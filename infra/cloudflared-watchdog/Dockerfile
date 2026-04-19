FROM golang:1.25.0 AS build

WORKDIR /src

COPY infra/cloudflared-watchdog/go.mod ./
COPY infra/cloudflared-watchdog/main.go ./

RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -o /out/cloudflared-watchdog .

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/cloudflared-watchdog /cloudflared-watchdog

ENTRYPOINT ["/cloudflared-watchdog"]
