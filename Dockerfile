FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM registry:3 AS distribution

FROM golang:1.26.10-alpine AS server-build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN GOPROXY="${GOPROXY}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mini-harbor ./cmd/server \
    && GOPROXY="${GOPROXY}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mini-harbor-gateway ./cmd/gateway

FROM alpine:3.23
RUN addgroup -S app && adduser -S -G app app \
    && mkdir -p /var/lib/mini-harbor /var/lib/registry-proxy \
    && chown app:app /var/lib/mini-harbor /var/lib/registry-proxy
WORKDIR /app
COPY --from=server-build /mini-harbor /usr/local/bin/mini-harbor
COPY --from=server-build /mini-harbor-gateway /usr/local/bin/mini-harbor-gateway
COPY --from=distribution /bin/registry /usr/local/bin/registry
COPY --from=distribution /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=web-build /src/web/dist ./web
ENV LISTEN_ADDR=:8080 \
    REGISTRY_URL=http://registry:5000 \
    REGISTRY_PUBLIC_URL=localhost:5000 \
    RQLITE_URLS=http://rqlite:4001 \
    IMAGE_USAGE_ROOT=/var/lib/mini-harbor/image-usage \
    WEB_ROOT=/app/web
EXPOSE 5000 8080
USER app
ENTRYPOINT ["mini-harbor"]
