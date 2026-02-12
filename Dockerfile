# Multi-stage build for minimal production image

# Build stage
FROM golang:1.25.7-alpine@sha256:f6751d823c26342f9506c03797d2527668d095b0a15f1862cddb4d927a7a4ced AS builder

ARG VERSION=dev
ARG GIT_COMMIT=unknown

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

RUN BUILD_TIME=$(date -u '+%Y-%m-%dT%H:%M:%SZ') && \
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME} -s -w" \
    -o ssv-oracle \
    ./cmd/oracle

# Create config directory
RUN mkdir -p /config

# Runtime stage - scratch for minimal attack surface
FROM scratch

LABEL org.opencontainers.image.title="SSV Oracle" \
      org.opencontainers.image.description="Oracle client for SSV Network cluster balance updates" \
      org.opencontainers.image.vendor="SSV Labs" \
      org.opencontainers.image.source="https://github.com/ssvlabs/ssv-oracle"

# Copy CA certificates for HTTPS connections
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=builder /build/ssv-oracle /usr/local/bin/ssv-oracle
COPY --from=builder /config /config

# Run as non-root user (numeric UID/GID for scratch)
USER 1000:1000

ENTRYPOINT ["/usr/local/bin/ssv-oracle"]
CMD ["run", "--config", "/config/config.yaml"]
