FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build cmd/pi/main.go -o pi .

FROM alpine:latest
RUN apk --no-cache ca-certificates
WORKDIR /root/
COPY --from=builder /app/pi .
CMD ["./pi"]
