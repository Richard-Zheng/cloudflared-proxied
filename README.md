# 带 SOCKS5 代理的 cloudflared

构建时先应用 `cloudflared_socks.patch`，再运行 `dns_patch.py`，最后使用上游的
`make cloudflared` 编译。`cloudflared/` 必须是未应用这两个补丁的原始源码，
宿主机不需要安装 Go 或 Python。

## Dockerfile 的区别

| 文件 | 用途 |
| --- | --- |
| `Dockerfile` | 使用 BuildKit / Buildx 自动选择目标架构，也支持构建多架构镜像。 |
| `Dockerfile.amd64` | 固定构建 Linux amd64，适用于常见 Intel / AMD 服务器。 |
| `Dockerfile.arm64` | 固定构建 Linux arm64，适用于 ARM 服务器。 |

三个文件均沿用上游的 Go 1.26.8 构建镜像和固定摘要的 Debian 13 distroless
运行镜像。最终镜像以 `65532:65532` 用户运行，只保留运行所需内容，不包含
Go、Python 和构建工具。保留上游的 `--no-autoupdate` 入口参数，默认显示版本。

上游的 `Dockerfile.fips.amd64` 和 `Dockerfile.fips.arm64` 是 FIPS 构建，
需要 Cloudflare 私有的 BoringCrypto Go 构建镜像，因此这里没有提供对应版本。

## 构建

在本目录运行，构建上下文必须是 `cloudflared_proxied/`，不能是里面的
`cloudflared/`。

amd64：

```sh
docker build -f Dockerfile.amd64 -t cloudflared-proxied:amd64 .
docker run --rm cloudflared-proxied:amd64
```

arm64：

```sh
docker build -f Dockerfile.arm64 -t cloudflared-proxied:arm64 .
```

固定架构的 Dockerfile 可在另一种架构的 Linux 主机上交叉编译；运行生成的
镜像仍需要相应架构的主机或模拟器。

安装了 Docker Buildx 时，也可以使用通用 Dockerfile：

```sh
docker buildx build --platform linux/amd64 -t cloudflared-proxied:amd64 --load .
docker buildx build --platform linux/arm64 -t cloudflared-proxied:arm64 --load .
```

可通过 `--build-arg VERSION=自定义版本号` 覆盖版本，默认由上游 Makefile
从源码的 Git 信息生成。模块下载默认使用公共 Go 模块代理，也可通过
`--build-arg GOPROXY=https://你的模块代理,direct` 覆盖。

## 通过代理运行

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
