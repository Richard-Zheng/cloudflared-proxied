#!/usr/bin/env python3
"""Insert DNS hooks into unpatched cloudflared sources; apply cloudflared_socks.patch separately.

Usage: python3 dns_patch.py [source-dir]

ALL_PROXY (or all_proxy) enables DoT to 1.1.1.1:853 through SOCKS5.
Use an IP literal for the proxy endpoint to avoid recursive DNS resolution.
NO_PROXY does not bypass the DNS proxy. Without a proxy, DNS is unchanged.
"""

import argparse
from pathlib import Path
import re
import shutil
import subprocess
import sys


MAIN_HOOK = '''
	proxyAddr := os.Getenv("ALL_PROXY")
	if proxyAddr == "" {
		proxyAddr = os.Getenv("all_proxy")
	}
	if proxyAddr != "" {
		net.DefaultResolver = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				proxyURL, err := url.Parse(proxyAddr)
				if err != nil {
					return nil, fmt.Errorf("invalid DNS proxy URL")
				}
				if net.ParseIP(proxyURL.Hostname()) == nil {
					return nil, fmt.Errorf("DNS proxy endpoint must use an IP literal")
				}
				proxyDialer, err := proxy.FromURL(proxyURL, &net.Dialer{Timeout: 15 * time.Second})
				if err != nil {
					return nil, fmt.Errorf("create DNS proxy dialer: %w", err)
				}
				dialer, ok := proxyDialer.(proxy.ContextDialer)
				if !ok {
					return nil, fmt.Errorf("DoT proxy dialer does not support context")
				}
				conn, err := dialer.DialContext(ctx, "tcp", "1.1.1.1:853")
				if err != nil {
					return nil, err
				}
				return tls.Client(conn, &tls.Config{
					ServerName: "cloudflare-dns.com",
					MinVersion: tls.VersionTLS12,
				}), nil
			},
		}
	}
'''

DOT_HOOK = '''
	if os.Getenv("ALL_PROXY") != "" || os.Getenv("all_proxy") != "" {
		ctx, cancel := context.WithTimeout(context.Background(), dotTimeout)
		defer cancel()
		return net.DefaultResolver.LookupSRV(ctx, srvService, srvProto, srvName)
	}
'''

PEEK_HOOK = '''
	if net.DefaultResolver.Dial != nil {
		r.network = network
		r.address = address
		return net.DefaultResolver.Dial(ctx, network, address)
	}
'''

# Hide Go comments and literals before locating declarations, preserving offsets.
NON_CODE = re.compile(
    r'//[^\n]*|/\*[\s\S]*?\*/|"(?:\\.|[^"\\])*"|'
    r"'(?:\\.|[^'\\])*'|`[^`]*`"
)


def mask(source, comments_only=False):
    def replace(match):
        if comments_only and not match[0].startswith(("//", "/*")):
            return match[0]
        return re.sub(r"[^\n]", " ", match[0])

    return NON_CODE.sub(replace, source)


def function_body(source, names):
    # These target functions have ordinary signatures, with no interface/struct
    # literals in their argument or result types. Reject ambiguous declarations.
    pattern = (
        r"(?m)^func\s+(?:\([^{}]*\)\s+)?(?:"
        + "|".join(map(re.escape, names))
        + r")\s*\([^{}]*\)\s*\{"
    )
    matches = list(re.finditer(pattern, mask(source)))
    if len(matches) != 1:
        raise ValueError(f"expected exactly one function {names}, found {len(matches)}")
    offset = matches[0].end()
    body = mask(source)[offset:]
    depth = 1
    for index, char in enumerate(body):
        depth += (char == "{") - (char == "}")
        if depth == 0:
            return offset, offset + index
    raise ValueError(f"function {names} has an unclosed body")


def insert_hook(source, names, hook):
    start, _ = function_body(source, names)
    if source[start:].startswith(hook):
        raise ValueError(f"function {names} is already patched; use unpatched sources")
    return source[:start] + hook + source[start:]


def add_imports(source, packages):
    # cloudflared uses an import block; fail rather than guess if that changes.
    matches = list(re.finditer(r"(?m)^import\s*\(", mask(source)))
    if len(matches) != 1:
        raise ValueError("expected exactly one import block")
    start = matches[0].end()
    end = mask(source).find(")", start)
    if end < 0:
        raise ValueError("unclosed import block")
    imports = mask(source[start:end], comments_only=True)
    missing = []
    for package in packages:
        found = re.search(r'(?m)^\s*(?:(\w+|\.)\s+)?"' + re.escape(package) + r'"', imports)
        if found is None:
            missing.append(package)
        elif found[1] not in (None, package.rsplit("/", 1)[-1]):
            raise ValueError(f"import {package} has unsupported alias {found[1]}")
    return source[:start] + "".join(f'\n\t"{p}"' for p in missing) + source[start:]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", nargs="?", type=Path, default=Path("."))
    args = parser.parse_args()
    targets = [
        "cmd/cloudflared/main.go",
        "edgediscovery/allregions/discovery.go",
        "ingress/origins/dns.go",
    ]
    pending = []
    gofmt = shutil.which("gofmt")
    # Validate and prepare all files before writing any of them.
    for relative in targets:
        path = args.source / relative
        original = path.read_text(encoding="utf-8")
        if relative == targets[0]:
            updated = add_imports(
                insert_hook(original, ("main",), MAIN_HOOK),
                ("context", "crypto/tls", "fmt", "net", "net/url", "os", "time", "golang.org/x/net/proxy"),
            )
        elif relative == targets[1]:
            updated = add_imports(
                insert_hook(original, ("lookupSRVWithDOT",), DOT_HOOK), ("os",)
            )
        else:
            updated = insert_hook(original, ("peekDial",), PEEK_HOOK)
        if gofmt:
            updated = subprocess.run(
                [gofmt], input=updated, text=True, capture_output=True, check=True
            ).stdout
        pending.append((path, updated))
    for path, updated in pending:
        path.write_text(updated, encoding="utf-8")
        print(f"patched: {path}")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        sys.exit(f"error: {error}")
