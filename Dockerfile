# Multi-stage build for minimal image size

# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build with version info
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

RUN go build \
    -ldflags="-X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME} -s -w" \
    -o ssv-oracle \
    ./cmd/oracle

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Create non-root user
RUN addgroup -g 1000 oracle && \
    adduser -D -u 1000 -G oracle oracle

# Create config directory
RUN mkdir -p /config && chown oracle:oracle /config

# Copy binary from builder
COPY --from=builder /build/ssv-oracle /usr/local/bin/ssv-oracle

# Switch to non-root user
USER oracle

# Set working directory
WORKDIR /home/oracle

# Expose any ports if needed (currently none)
# EXPOSE 8080

# Default command
ENTRYPOINT ["ssv-oracle"]
CMD ["run", "--config", "/config/config.yaml"]
