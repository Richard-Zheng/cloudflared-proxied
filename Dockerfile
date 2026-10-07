FROM --platform=$BUILDPLATFORM golang:1.26.8 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG GOPROXY=https://proxy.golang.org,direct

ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    GOPROXY=${GOPROXY} \
    CONTAINER_BUILD=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends python3 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /go/src/github.com/cloudflare/cloudflared/

COPY cloudflared/go.mod cloudflared/go.sum ./
RUN go mod download

COPY cloudflared/ ./
COPY dns_patch.py cloudflared_socks.patch /patches/

RUN git apply /patches/cloudflared_socks.patch \
    && python3 /patches/dns_patch.py .

RUN if [ -n "${VERSION}" ]; then \
        make cloudflared TARGET_OS="${TARGETOS}" TARGET_ARCH="${TARGETARCH}" VERSION="${VERSION}"; \
    else \
        make cloudflared TARGET_OS="${TARGETOS}" TARGET_ARCH="${TARGETARCH}"; \
    fi

FROM gcr.io/distroless/base-debian13:nonroot@sha256:0896741ba5bafd3ac87ea025a5f578952f2d238ddc3614cb368acc983a687aa2

LABEL org.opencontainers.image.source="https://github.com/cloudflare/cloudflared"

COPY --from=builder --chown=65532:65532 /go/src/github.com/cloudflare/cloudflared/cloudflared /usr/local/bin/

USER 65532:65532

ENTRYPOINT ["cloudflared", "--no-autoupdate"]
CMD ["version"]
