# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache module downloads separately from source
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /gateway \
    ./cmd/gateway

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12 AS runtime

COPY --from=builder /gateway /gateway
COPY config/config.example.yaml /config/config.yaml

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/gateway"]
