# Stage 1: Build the static Tidy binary
FROM golang:1.27.1-alpine AS builder

WORKDIR /build

# Cache Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build statically linked stripped binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /build/tidy main.go

# Stage 2: Runtime image targeting Java 25 on Pterodactyl Yolks
FROM ghcr.io/pterodactyl/yolks:java_25

LABEL org.opencontainers.image.title="Tidy Orchestrator"
LABEL org.opencontainers.image.description="Pterodactyl container image with Java 25, Git, and Tidy orchestrator"
LABEL org.opencontainers.image.authors="Kenzi Siaufandi <github.com/Kenzi-Siaufandi>"
LABEL org.opencontainers.image.source="https://github.com/Kenzi-Siaufandi/tidy"
LABEL org.opencontainers.image.licenses="MIT"

USER root

# Ensure essential tools and latest CA certificates are installed
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        git \
        ca-certificates \
        curl \
        tar \
        tzdata \
        iproute2 && \
    rm -rf /var/lib/apt/lists/*

# Install pre-compiled Tidy binary into system PATH
COPY --from=builder /build/tidy /usr/local/bin/tidy
COPY entrypoint-tidy.sh /entrypoint-tidy.sh
RUN chmod +x /usr/local/bin/tidy /entrypoint-tidy.sh

# Set up standard Pterodactyl container environment
USER container
ENV USER=container HOME=/home/container
WORKDIR /home/container

# Auto pre-flight: run tidy, then exec $STARTUP (Egg startup stays pure java).
# Stock Yolks `exec env ${PARSED}` mangles `&&`/`;`/`$()`, so `tidy && java`
# in STARTUP cannot work — hence the eval-based wrapper above.
CMD ["/bin/bash", "/entrypoint-tidy.sh"]
