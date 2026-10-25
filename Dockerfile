# Build-time mirrors. The defaults point at mirrors that are reachable from
# mainland China; override them for other networks, e.g.
#   docker buildx build --build-arg GOPROXY=https://proxy.golang.org,direct .
ARG GOPROXY=https://goproxy.cn,https://goproxy.io,direct
ARG NPM_REGISTRY=https://registry.npmmirror.com

FROM node:22-alpine AS web-build
ARG NPM_REGISTRY
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --registry=${NPM_REGISTRY}
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS go-build
ARG GOPROXY
ARG VERSION=v1.0.1
ENV GOPROXY=${GOPROXY}
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.platformVersion=${VERSION}" -o /out/ftc-platform ./cmd/platform

FROM alpine:3.22
RUN apk add --no-cache ca-certificates kubectl && addgroup -S ftc && adduser -S -G ftc -u 10001 ftc
WORKDIR /app
COPY --from=go-build /out/ftc-platform /app/ftc-platform
COPY --from=web-build /src/web/dist /app/web
RUN mkdir -p /app/data && chown -R ftc:ftc /app
USER 10001
ENV LISTEN_ADDR=:8080 WEB_DIST_DIR=/app/web DATABASE_PATH=/app/data/platform.db BOOTSTRAP_TOKEN_PATH=/app/data/bootstrap-token
EXPOSE 8080 8081 8443
ENTRYPOINT ["/app/ftc-platform"]

