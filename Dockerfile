# Build stage
FROM golang:1.27-alpine AS builder

ENV GOTOOLCHAIN=auto

# ca-certificates is needed here only so it can be copied into the scratch
# final stage below - the builder itself does not need to make TLS calls.
RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build \
    -tags 'netgo osusergo static_build' \
    -trimpath \
    -ldflags="-w -s -extldflags '-static -fno-PIC'" \
    -o proxy ./cmd/proxy

RUN go build \
    -tags 'netgo osusergo static_build' \
    -trimpath \
    -ldflags="-w -s -extldflags '-static -fno-PIC'" \
    -o healthcheck ./cmd/healthcheck

# Final stage
FROM scratch

WORKDIR /app

# CA certificates for verified TLS to Tesla's cloud/Fleet APIs (backend/cloud
# and backend/fleetapi do not skip certificate verification, unlike the
# local gateway backends, which talk to a self-signed device on the LAN).
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=builder /app/proxy .
COPY --from=builder /app/healthcheck .

# pypowerwall proxy default port
EXPOSE 8675

LABEL org.opencontainers.image.source="https://github.com/blackbirdworks/gopowerwall"

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/healthcheck"]

# Run as a fixed non-root UID/GID. scratch has no /etc/passwd, but Linux
# does not require a passwd entry to run a process under a given numeric
# UID/GID - only to resolve that UID to a username, which nothing here needs.
USER 65532:65532

CMD ["./proxy"]
