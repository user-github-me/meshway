ARG GO_IMAGE=golang:1.27.2-alpine
ARG RUNTIME_IMAGE=alpine:3.24.2
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
WORKDIR /src
COPY go.mod go.sum ./
RUN test -n "$GOSUMDB" && test "$GOSUMDB" != off && go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-$(go env GOARCH)}" go build -trimpath -ldflags='-s -w' -o /out/meshway ./cmd/meshway

FROM ${RUNTIME_IMAGE}
RUN apk add --no-cache ca-certificates && addgroup -S meshway && adduser -S -G meshway meshway && mkdir -p /var/lib/meshway && chown meshway:meshway /var/lib/meshway
COPY --from=build /out/meshway /usr/local/bin/meshway
USER meshway
WORKDIR /var/lib/meshway
VOLUME ["/var/lib/meshway"]
EXPOSE 4001/tcp 4001/udp
ENTRYPOINT ["meshway"]
CMD ["daemon", "--config", "/var/lib/meshway/config.json"]
