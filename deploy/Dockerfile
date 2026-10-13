FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/meshway ./cmd/meshway

FROM alpine:3.24.2
RUN apk add --no-cache ca-certificates && addgroup -S meshway && adduser -S -G meshway meshway && mkdir -p /var/lib/meshway && chown meshway:meshway /var/lib/meshway
COPY --from=build /out/meshway /usr/local/bin/meshway
USER meshway
WORKDIR /var/lib/meshway
VOLUME ["/var/lib/meshway"]
EXPOSE 4001/tcp 4001/udp
ENTRYPOINT ["meshway"]
CMD ["daemon", "--config", "/var/lib/meshway/config.json"]
