FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ratelimiter ./cmd/ratelimiter

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
COPY --from=builder /ratelimiter /usr/local/bin/ratelimiter
ENTRYPOINT ["ratelimiter"]
CMD ["--config", "/etc/ratelimiter/config.yaml"]
