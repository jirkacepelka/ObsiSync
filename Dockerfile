# syntax=docker/dockerfile:1
# Multi-arch image (amd64, arm64) with the server and the Obsidian plugin.

FROM --platform=$BUILDPLATFORM node:22-alpine AS plugin
WORKDIR /src/plugin
COPY plugin/package.json plugin/package-lock.json ./
RUN npm ci
COPY plugin/ ./
RUN node esbuild.config.mjs production

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS server
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /obsisync ./cmd/obsisync

FROM gcr.io/distroless/static-debian12
COPY --from=server /obsisync /usr/local/bin/obsisync
COPY --from=plugin /src/plugin/dist /app/plugin
ENV OBSISYNC_DATA=/data OBSISYNC_ADDR=:8080 OBSISYNC_PLUGIN_DIR=/app/plugin
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["obsisync", "healthcheck"]
ENTRYPOINT ["obsisync"]
CMD ["serve"]
