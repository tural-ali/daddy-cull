FROM node:24.13.1-bookworm-slim AS web
WORKDIR /build
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-alpine3.24 AS go
# go-sqlite3 is cgo, so it is compiled against the same musl the runtime uses.
RUN apk add --no-cache build-base
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
# The Cull Sync sources are compiled into the binary, which serves them to the
# Mac that runs the setup command.
COPY mac/ ./mac/
# VERSION is the release this is, from tools/version.sh; see docs/SERVER.md.
ARG VERSION=dev
RUN CGO_ENABLED=1 go test ./... \
 && go build -trimpath -ldflags "-X main.version=${VERSION}" -o /out/cull ./cmd/cull \
 && go build -trimpath -o /out/scale ./cmd/scale

FROM alpine:3.24
# ffmpeg decodes video frames and HEIC stills. An iPhone HEIC is a tiled grid
# with an HDR gain map, and ffmpeg before 8.1 returns the wrong picture for it:
# for a 5712x4284 photograph 7.1 gave a black 2016x1512 frame and 8.0 the same
# wrong size, so the runtime needs a distribution that packages 8.1. exiftool reads the JPEG a
# camera embeds in a RAW file.
RUN apk add --no-cache ffmpeg exiftool \
 && addgroup -g 10001 cull && adduser -D -H -u 10001 -G cull cull
WORKDIR /app
COPY --from=go /out/cull /out/scale /app/
COPY --from=web /build/dist /app/web/dist
USER 10001:10001
EXPOSE 8830
ENTRYPOINT ["/app/cull"]
CMD ["-db","/state/scale.db","-listen","0.0.0.0:8830","-demo-network"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD ["/app/cull","-check"]
