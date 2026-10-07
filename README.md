# 带 SOCKS5 代理的 cloudflared

本仓库让 cloudflared 支持 `ALL_PROXY=socks5://127.0.0.1:7080` 走前置代理访问 Cloudflare 边缘节点，以改善直连不佳的情况。

`patch_cloudflared.go` 使用 Go AST 定位函数和拨号调用。
实际的代理实现和回归测试在 `overlay/`；
环境变量读取、DNS 和 TCP Dial 逻辑集中在 `internal/proxyenv` 包中。

工具会先验证所有目标、格式化所有修改，再开始写文件。`--check` 只检查并列出
待修改的文件。函数缺失、参数变化、调用不唯一或已经应用修改时会报错，不会写入。
当前已验证上游提交 `18cdfe0a6fc7b72a0702d255a1f984e776ce0498`。

## 构建

```sh
git clone --depth 1 https://github.com/cloudflare/cloudflared
git clone https://github.com/Richard-Zheng/cloudflared-proxied

go run cloudflared-proxied/patch_cloudflared.go --check ./cloudflared
go run cloudflared-proxied/patch_cloudflared.go ./cloudflared
cd cloudflared

make cloudflared
```

应用 patch 后可运行代理回归测试（先移除测试进程的代理环境变量，避免影响上游测试）：

```sh
env -u ALL_PROXY -u all_proxy go test -mod=readonly ./internal/proxyenv ./connection ./cmd/cloudflared/tunnel
```

## 使用

[快速隧道 - try cloudflare](https://try.cloudflare.com/)

```sh
ALL_PROXY=socks5://127.0.0.1:7080 cloudflared tunnel --url http://localhost:8000 --output json
```

[本地管理的隧道](https://cloudflaredoc.ubitools.com/tunnel/advanced/local-management/)

```sh
ALL_PROXY=socks5://127.0.0.1:7080 cloudflared --config [config.yaml] --no-autoupdate tunnel run [name]
```

[远端管理的隧道](https://cloudflaredoc.ubitools.com/tunnel/advanced/run-parameters/)

```sh
ALL_PROXY=socks5://127.0.0.1:7080 cloudflared --no-autoupdate tunnel run --token <TOKEN_VALUE>
```

## Docker 容器

同时提供 Docker 容器版本。

```sh
docker run --rm \
  -e ALL_PROXY=socks5://192.168.1.10:1080 \
  -e TUNNEL_TOKEN \
  cloudflared-proxied:amd64 tunnel run
```

先在宿主机设置 `TUNNEL_TOKEN`。代理地址需要是容器可访问的 IP；容器内的
`127.0.0.1` 指向容器自身。支持 `ALL_PROXY` 和 `all_proxy`，优先使用前者。

设置代理后，cloudflared 的 DNS 解析通过 SOCKS5 连接 `1.1.1.1:853`，使用
校验证书的 DoT，不受 `NO_PROXY` 绕过，不需要设置 DNS 环境变量。代理服务器
需要允许访问该目标。没有设置代理时，DNS 保持上游逻辑。

设置 `ALL_PROXY` 或 `all_proxy` 后，隧道强制使用 HTTP/2，无需手动指定
`--protocol http2`。即使指定 `--protocol quic`，也会使用 HTTP/2，且不会回退到
QUIC。代理模式同时跳过会发起直连 QUIC 请求的启动连通性预检查。
没有设置代理时，协议选择和预检查保持上游逻辑。
