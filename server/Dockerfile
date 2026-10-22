FROM golang:1.25-alpine AS build
# 国内网络默认走 goproxy.cn，可用 --build-arg GOPROXY=... 覆盖
ARG GOPROXY=https://goproxy.cn,https://goproxy.io,direct
ENV GOPROXY=${GOPROXY}
# go.mod 的 go 指令可能高于基础镜像自带的工具链（本机就是 1.26 vs 镜像 1.25），
# 让 Go 自己按需下载对应工具链，别用 GOTOOLCHAIN=local 硬顶。
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache tzdata ca-certificates wget \
 && adduser -D -u 10001 app \
 && mkdir -p /data/storage && chown -R app:app /data
ENV TZ=Asia/Shanghai
WORKDIR /app
COPY --from=build /out/api /app/api
USER app
EXPOSE 18090
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
  CMD wget -qO- http://127.0.0.1:18090/healthz || exit 1
ENTRYPOINT ["/app/api"]
