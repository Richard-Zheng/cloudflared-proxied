# 带 SOCKS5 代理的 cloudflared

本仓库包含对 cloudflared 的 patch, 目的是让 cloudflared 支持 `ALL_PROXY=socks5://127.0.0.1:7080` 走前置代理访问 Cloudflare 边缘节点，以改善直连不佳的情况。

构建时先应用 `cloudflared_socks.patch`，再运行 `dns_patch.py`，最后使用上游的
`make cloudflared` 编译。`cloudflared/` 必须是未应用这两个补丁的原始源码。

## 构建

```sh
git clone --depth 1 https://github.com/cloudflare/cloudflared
git clone https://github.com/Richard-Zheng/cloudflared-proxied

python3 ./cloudflared-proxied/dns_patch.py ./cloudflared
cp ./cloudflared-proxied/cloudflared_socks.patch cloudflared/
cd cloudflared
git apply cloudflared_socks.patch

make cloudflared
```

## 使用

[快速隧道 - try cloudflare](https://try.cloudflare.com/)

```sh
ALL_PROXY=socks5://127.0.0.1:7080 cloudflared tunnel --url http://localhost:8000 --output json
```

[本地管理的隧道](https://cloudflaredoc.ubitools.com/tunnel/advanced/local-management/)

```sh
ALL_PROXY=socks5://127.0.0.1:7080 cloudflared --protocol http2 --config [config.yaml] --no-autoupdate tunnel run [name]
```

[远端管理的隧道](https://cloudflaredoc.ubitools.com/tunnel/advanced/run-parameters/)

```sh
ALL_PROXY=socks5://127.0.0.1:7080 cloudflared --protocol http2 --no-autoupdate tunnel run --token <TOKEN_VALUE>
```

## Docker 容器

同时提供 Docker 容器版本。

```sh
docker run --rm \
  -e ALL_PROXY=socks5://192.168.1.10:1080 \
  -e TUNNEL_TOKEN \
  cloudflared-proxied:amd64 tunnel --protocol http2 run
```

先在宿主机设置 `TUNNEL_TOKEN`。代理地址需要是容器可访问的 IP；容器内的
`127.0.0.1` 指向容器自身。支持 `ALL_PROXY` 和 `all_proxy`，优先使用前者。

设置代理后，cloudflared 的 DNS 解析通过 SOCKS5 连接 `1.1.1.1:853`，使用
校验证书的 DoT，不受 `NO_PROXY` 绕过，不需要设置 DNS 环境变量。代理服务器
需要允许访问该目标。没有设置代理时，DNS 保持上游逻辑。

隧道应使用 `--protocol http2`：现有代理补丁修改的是 TCP edge 连接，
没有为 QUIC 的 UDP 连接添加 SOCKS5 支持。
