# Multi-stage build for minimal production image

# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

# Extract version info from git (works automatically, no build args needed)
RUN VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev") && \
    GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown") && \
    BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ') && \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME} -s -w" \
    -o ssv-oracle \
    ./cmd/oracle

# Runtime stage
FROM alpine:3.20

LABEL org.opencontainers.image.title="SSV Oracle" \
      org.opencontainers.image.description="Oracle client for SSV Network cluster balance updates" \
      org.opencontainers.image.vendor="SSV Labs" \
      org.opencontainers.image.source="https://github.com/ssvlabs/ssv-oracle"

RUN apk --no-cache add ca-certificates && \
    addgroup -g 1000 oracle && \
    adduser -D -u 1000 -G oracle oracle && \
    mkdir /config && chown oracle:oracle /config

COPY --from=builder /build/ssv-oracle /usr/local/bin/

USER oracle
WORKDIR /home/oracle

ENTRYPOINT ["ssv-oracle"]
CMD ["run", "--config", "/config/config.yaml"]
