# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

# Install required build tools
RUN apk add --no-cache git

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o cfctl .

# Final stage
FROM alpine:3.19

WORKDIR /app

# Install CA certificates for HTTPS
RUN apk --no-cache add ca-certificates

# Copy binary from builder
COPY --from=builder /app/cfctl .

# Create directory for configuration
RUN mkdir -p /root/.spaceone

# Set environment variable
ENV CFCTL_DEFAULT_ENVIRONMENT=default

# Set entrypoint
ENTRYPOINT ["/app/cfctl"]

