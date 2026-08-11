# syntax=docker/dockerfile:1

# --- Build stage: static Linux binary ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -buildvcs=false -ldflags='-s -w' \
    -o /out/emby-service-portal ./cmd/emby-service-portal

# --- Runtime stage: minimal image with CA certificates and IANA time zones ---
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 -H -s /sbin/nologin app
COPY --from=build /out/emby-service-portal /usr/local/bin/emby-service-portal
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/emby-service-portal"]
