# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN VERSION_VALUE="${VERSION:-v$(cat VERSION)}" \
    && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION_VALUE} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/piccolo ./cmd/piccolo

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && addgroup -S piccolo \
    && adduser -S -G piccolo piccolo
WORKDIR /app
COPY --from=builder /out/piccolo ./piccolo
USER piccolo
EXPOSE 7789
ENTRYPOINT ["./piccolo"]
CMD ["--help"]
