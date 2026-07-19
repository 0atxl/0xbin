# syntax=docker/dockerfile:1

FROM node:25-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm install --global npm@11.6.2 && npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS server-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-build /src/internal/webassets/dist ./internal/webassets/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/0xbin ./cmd/0xbin

FROM alpine:3.22
RUN addgroup -S 0xbin && adduser -S -G 0xbin -h /nonexistent 0xbin \
    && mkdir /data && chown 0xbin:0xbin /data
COPY --from=server-build /out/0xbin /usr/local/bin/0xbin
USER 0xbin
ENV OXBIN_LISTEN_ADDR=0.0.0.0:8080 \
    OXBIN_DATA_DIR=/data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:8080/health/live || exit 1
ENTRYPOINT ["/usr/local/bin/0xbin"]
