package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

//go:embed overlay
var overlay embed.FS

const proxyImport = "github.com/cloudflare/cloudflared/internal/proxyenv"

type edit struct {
	start int
	end   int
	text  string
}

type target struct {
	path     string
	function string
	hook     string
	params   map[string]string
	dial     bool
}

type change struct {
	path string
	data []byte
	mode fs.FileMode
}

func main() {
	check := flag.Bool("check", false, "validate and list changes without writing files")
	flag.Parse()
	if flag.NArg() > 1 {
		fail(fmt.Errorf("usage: go run patch_cloudflared.go [--check] [source-dir]"))
	}
	source := "."
	if flag.NArg() == 1 {
		source = flag.Arg(0)
	}
	changes, err := prepare(source)
	if err != nil {
		fail(err)
	}
	for _, pending := range changes {
		if *check {
			fmt.Printf("would patch: %s\n", pending.path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(pending.path), 0755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(pending.path, pending.data, pending.mode); err != nil {
			fail(err)
		}
		fmt.Printf("patched: %s\n", pending.path)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func prepare(source string) ([]change, error) {
	module, err := os.ReadFile(filepath.Join(source, "go.mod"))
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(string(module))
	if len(fields) < 2 || fields[0] != "module" || fields[1] != "github.com/cloudflare/cloudflared" {
		return nil, fmt.Errorf("expected cloudflared sources at %s", source)
	}
	targets := []target{
		{path: "cmd/cloudflared/main.go", function: "main", hook: "proxyenv.ConfigureDNS()"},
		{path: "connection/protocol.go", function: "NewProtocolSelector", params: map[string]string{"protocolFlag": "string"},
			hook: "if proxyenv.Enabled() {\nprotocolFlag = HTTP2.String()\n}"},
		{path: "cmd/cloudflared/tunnel/cmd.go", function: "runPrechecks", hook: "if proxyenv.Enabled() {\nreturn\n}"},
		{path: "edgediscovery/dial.go", function: "DialEdge", dial: true},
		{path: "edgediscovery/allregions/discovery.go", function: "lookupSRVWithDOT",
			params: map[string]string{"srvService": "string", "srvProto": "string", "srvName": "string"},
			hook:   "if proxyenv.Enabled() {\nreturn proxyenv.LookupSRV(srvService, srvProto, srvName)\n}"},
		{path: "ingress/origins/dns.go", function: "peekDial", params: map[string]string{"ctx": "context.Context", "network": "string", "address": "string"},
			hook: "if proxyenv.Enabled() {\nr.network = network\nr.address = address\nreturn proxyenv.DialDNS(ctx, network, address)\n}"},
	}
	var changes []change
	for _, spec := range targets {
		path := filepath.Join(source, filepath.FromSlash(spec.path))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		updated, err := transform(path, data, spec)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", spec.path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		changes = append(changes, change{path: path, data: updated, mode: info.Mode().Perm()})
	}
	err = fs.WalkDir(overlay, "overlay", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		destination := filepath.Join(source, filepath.FromSlash(strings.TrimPrefix(path, "overlay/")))
		if _, err := os.Lstat(destination); err == nil {
			return fmt.Errorf("%s already exists; use unpatched sources", destination)
		} else if !os.IsNotExist(err) {
			return err
		}
		data, err := overlay.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		changes = append(changes, change{path: destination, data: formatted, mode: 0644})
		return nil
	})
	return changes, err
}

func transform(path string, source []byte, spec target) ([]byte, error) {
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, path, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var importBlock *ast.GenDecl
	for _, declaration := range file.Decls {
		if block, ok := declaration.(*ast.GenDecl); ok && block.Tok == token.IMPORT && block.Lparen.IsValid() && importBlock == nil {
			importBlock = block
		}
	}
	if importBlock == nil {
		return nil, fmt.Errorf("expected an import block")
	}
	for _, imported := range file.Imports {
		name, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			return nil, err
		}
		if name == proxyImport {
			return nil, fmt.Errorf("already patched; use unpatched sources")
		}
		if name == "proxyenv" || strings.HasSuffix(name, "/proxyenv") || imported.Name != nil && imported.Name.Name == "proxyenv" {
			return nil, fmt.Errorf("proxyenv import name is already in use")
		}
	}
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == spec.function {
			if function != nil {
				return nil, fmt.Errorf("ambiguous function %s", spec.function)
			}
			function = candidate
		}
	}
	if function == nil || function.Body == nil {
		return nil, fmt.Errorf("missing function %s", spec.function)
	}
	for required, expectedType := range spec.params {
		found := false
		for _, parameter := range function.Type.Params.List {
			for _, name := range parameter.Names {
				found = found || name.Name == required && typeName(parameter.Type) == expectedType
			}
		}
		if !found {
			return nil, fmt.Errorf("%s no longer has parameter %s of type %s", spec.function, required, expectedType)
		}
	}
	if spec.function == "peekDial" {
		if function.Recv == nil || len(function.Recv.List) != 1 || len(function.Recv.List[0].Names) != 1 || function.Recv.List[0].Names[0].Name != "r" || typeName(function.Recv.List[0].Type) != "*resolver" {
			return nil, fmt.Errorf("peekDial receiver has changed")
		}
	}
	offset := positions.File(file.Pos()).Offset
	edits := []edit{{start: offset(importBlock.Lparen) + 1, end: offset(importBlock.Lparen) + 1,
		text: "\n\t" + strconv.Quote(proxyImport)}}
	if spec.dial {
		var calls []*ast.CallExpr
		dialerDeclarations := 0
		ast.Inspect(function.Body, func(node ast.Node) bool {
			if assignment, ok := node.(*ast.AssignStmt); ok && assignment.Tok == token.DEFINE && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
				name, named := assignment.Lhs[0].(*ast.Ident)
				value, composite := assignment.Rhs[0].(*ast.CompositeLit)
				if named && name.Name == "dialer" && composite && typeName(value.Type) == "net.Dialer" {
					dialerDeclarations++
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "DialContext" {
				return true
			}
			receiver, ok := selector.X.(*ast.Ident)
			if ok && receiver.Name == "dialer" {
				calls = append(calls, call)
			}
			return true
		})
		if dialerDeclarations != 1 || len(calls) != 1 || len(calls[0].Args) != 3 || calls[0].Ellipsis.IsValid() {
			return nil, fmt.Errorf("expected one dialer.DialContext call with three arguments in DialEdge")
		}
		call := calls[0]
		edits = append(edits,
			edit{start: offset(call.Fun.Pos()), end: offset(call.Fun.End()), text: "proxyenv.DialContext"},
			edit{start: offset(call.Args[2].End()), end: offset(call.Args[2].End()), text: ", &dialer"})
	} else {
		edits = append(edits, edit{start: offset(function.Body.Lbrace) + 1, end: offset(function.Body.Lbrace) + 1,
			text: "\n\t" + spec.hook + "\n"})
	}
	sort.Slice(edits, func(first, second int) bool { return edits[first].start > edits[second].start })
	updated := bytes.Clone(source)
	for _, replacement := range edits {
		updated = append(append(append([]byte{}, updated[:replacement.start]...), replacement.text...), updated[replacement.end:]...)
	}
	return format.Source(updated)
}

func typeName(expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return expression.Name
	case *ast.SelectorExpr:
		return typeName(expression.X) + "." + expression.Sel.Name
	case *ast.StarExpr:
		return "*" + typeName(expression.X)
	default:
		return ""
	}
}
