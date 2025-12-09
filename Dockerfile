# Multi-stage build for minimal production image

# Global build arguments
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_TIME=unknown

# Build stage
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

ARG VERSION
ARG GIT_COMMIT
ARG BUILD_TIME

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-X main.Version=${VERSION} -X main.GitCommit=${GIT_COMMIT} -X main.BuildTime=${BUILD_TIME} -s -w" \
    -o ssv-oracle \
    ./cmd/oracle

# Runtime stage
FROM alpine:3.20

ARG VERSION
ARG GIT_COMMIT
ARG BUILD_TIME
LABEL org.opencontainers.image.title="SSV Oracle" \
      org.opencontainers.image.description="Oracle client for SSV Network cluster balance updates" \
      org.opencontainers.image.vendor="SSV Labs" \
      org.opencontainers.image.source="https://github.com/ssvlabs/ssv-oracle" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.created="${BUILD_TIME}"

RUN apk --no-cache add ca-certificates && \
    addgroup -g 1000 oracle && \
    adduser -D -u 1000 -G oracle oracle && \
    mkdir /config && chown oracle:oracle /config

COPY --from=builder /build/ssv-oracle /usr/local/bin/

USER oracle
WORKDIR /home/oracle

ENTRYPOINT ["ssv-oracle"]
CMD ["run", "--config", "/config/config.yaml"]

