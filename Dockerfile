FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -cover -coverpkg=github.com/gaarutyunov/mcp-anything/... -o /proxy ./cmd/proxy

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
# TIKTOKEN_CACHE_DIR points tiktoken-go to a writable, predictable cache directory.
# BPE encoding data is downloaded on first use if not already cached.
ENV TIKTOKEN_CACHE_DIR=/tmp/tiktoken_cache
# GOCOVERDIR is where the coverage-instrumented binary writes its data on exit.
# Bind-mount a host directory here to collect coverage from the container.
ENV GOCOVERDIR=/tmp/gocov
RUN mkdir -p /tmp/gocov
COPY --from=builder /proxy /usr/local/bin/proxy
ENTRYPOINT ["proxy"]
