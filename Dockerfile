# Build
FROM golang:1.25-alpine AS build
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/polling2webhook .

# Runtime: small image + CA bundle for Telegram HTTPS
FROM alpine:3.21
RUN apk add --no-cache ca-certificates && update-ca-certificates
COPY --from=build /out/polling2webhook /usr/local/bin/polling2webhook
USER nobody
ENTRYPOINT ["/usr/local/bin/polling2webhook"]
CMD ["-config", "/config/config.toml"]
