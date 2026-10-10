# wx-bot container image. Mount a config at /etc/wx-bot/config.json and
# persist /state (login session, cursor, inbox) and /media.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wx-bot ./cmd/wx-bot

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S bot && adduser -S -G bot -h /state bot && \
    mkdir -p /state /media /etc/wx-bot && chown bot:bot /state /media
COPY --from=build /out/wx-bot /usr/local/bin/wx-bot
COPY LICENSE /licenses/wx-bot-LICENSE
USER bot
ENV HOME=/state
VOLUME ["/state", "/media"]
ENTRYPOINT ["wx-bot"]
CMD ["--config", "/etc/wx-bot/config.json", "run"]
