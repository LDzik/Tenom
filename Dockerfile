FROM golang:alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /bin/tenom ./cmd/tenom

FROM alpine:latest

RUN apk --no-cache add \
    ca-certificates \
    tzdata \
    traceroute

WORKDIR /app

COPY --from=builder /bin/tenom /app/tenom

EXPOSE 7366

ENTRYPOINT ["/app/tenom"]
CMD ["-config", "/app/config/config.json"]