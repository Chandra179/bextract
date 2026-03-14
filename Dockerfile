# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.26-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
COPY vendor/ vendor/
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -mod=vendor -o bextract ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    chromium \
    fonts-liberation \
    libasound2 \
    libatk-bridge2.0-0 \
    libatk1.0-0 \
    libcups2 \
    libdbus-1-3 \
    libdrm2 \
    libgbm1 \
    libgtk-3-0 \
    libnspr4 \
    libnss3 \
    libx11-xcb1 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libxss1 \
    libxtst6 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /app/bextract .

EXPOSE 8080

# go-rod uses ROD_BROWSER_BIN to locate the browser binary; --no-sandbox is set programmatically
ENV ROD_BROWSER_BIN="/usr/bin/chromium"

ENTRYPOINT ["/app/bextract"]
