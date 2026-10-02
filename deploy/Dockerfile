FROM golang:1.21-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build -a -installsuffix cgo -ldflags="-s -w" -o twitch-bot ./cmd

# Final stage
FROM alpine:latest

# Install runtime dependencies
RUN apk add --no-cache ca-certificates curl sqlite-libs

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/twitch-bot .
COPY --from=builder /app/web ./web

# Create data directory
RUN mkdir -p /data

# Expose port
EXPOSE 9090

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=40s --retries=3 \
  CMD curl -f http://localhost:9090/api/health || exit 1

# Run the bot
ENTRYPOINT ["./twitch-bot", "-db=/data/bot.db"]
