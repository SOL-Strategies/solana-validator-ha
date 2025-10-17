FROM golang:1.25-alpine

WORKDIR /app

# Install ca-certificates
RUN apk --no-cache add ca-certificates

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Create config directory
RUN mkdir -p /config

# Expose port
EXPOSE 8989

# Run the Go program directly
CMD ["go", "run", "main.go"]
