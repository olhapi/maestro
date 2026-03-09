# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies for SQLite
RUN apk add --no-cache gcc musl-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY . .

# Build with CGO for SQLite
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o maestro ./cmd/maestro

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates

WORKDIR /app

# Copy binary
COPY --from=builder /app/maestro /usr/local/bin/maestro

# Create data directory
RUN mkdir -p /data

ENTRYPOINT ["maestro"]
CMD ["--help"]
