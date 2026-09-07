# Build stage
FROM golang:1.27-alpine AS builder

ENV GOTOOLCHAIN=auto

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build \
    -tags 'netgo osusergo static_build' \
    -trimpath \
    -ldflags="-w -s -extldflags '-static -fno-PIC'" \
    -o proxy ./cmd/proxy

# Final stage
FROM scratch

WORKDIR /app

COPY --from=builder /app/proxy .

# pypowerwall proxy default port
EXPOSE 8675

LABEL org.opencontainers.image.source="https://github.com/blackbirdworks/gopowerwall"

CMD ["./proxy"]
