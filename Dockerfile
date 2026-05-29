# ── Build stage ───────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app ./cmd/server

# ── Runtime stage ─────────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app .
COPY web/ ./web/

EXPOSE 8080

VOLUME ["/app/data"]

CMD ["./app"]
