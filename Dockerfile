FROM node:24-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26.10-alpine AS distribution
WORKDIR /src/distribution
ADD --checksum=sha256:db8d38f341b7699e516eaa33986814cce3276e75d1df4ac6f77d60e58c688c45 https://codeload.github.com/distribution/distribution/tar.gz/9a8d98b679740cd514aa7e7d84d23d442a5ef54c /tmp/distribution.tar.gz
RUN tar -xzf /tmp/distribution.tar.gz --strip-components=1 -C /src/distribution
COPY registry/_distribution/ordered-walk.patch /tmp/ordered-walk.patch
RUN apk add --no-cache patch && patch --fuzz=0 -p1 < /tmp/ordered-walk.patch
COPY registry/_distribution/*.go registry/storage/driver/s3-aws/
RUN go test ./registry/storage/driver/s3-aws -run '^TestOrderedWalk' -count=1 \
    && CGO_ENABLED=0 go build -trimpath \
       -ldflags='-s -w -X github.com/distribution/distribution/v3/version.version=v3.1.1-mini-harbor.1' \
       -o /bin/registry ./cmd/registry

FROM golang:1.26.10-alpine AS server-build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
COPY go.mod go.sum ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN GOPROXY="${GOPROXY}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mini-harbor ./cmd/server \
    && GOPROXY="${GOPROXY}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mini-harbor-gateway ./cmd/gateway \
    && GOPROXY="${GOPROXY}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mini-harbor-migrate ./cmd/migrate

FROM alpine:3.23
RUN addgroup -S app && adduser -S -G app app \
    && mkdir -p /var/lib/mini-harbor /var/lib/registry-proxy \
    && chown app:app /var/lib/mini-harbor /var/lib/registry-proxy
WORKDIR /app
COPY --from=server-build /mini-harbor /usr/local/bin/mini-harbor
COPY --from=server-build /mini-harbor-gateway /usr/local/bin/mini-harbor-gateway
COPY --from=server-build /mini-harbor-migrate /usr/local/bin/mini-harbor-migrate
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
