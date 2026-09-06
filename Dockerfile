FROM node:24.13.1-bookworm-slim AS web
WORKDIR /build
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-bookworm AS go
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=1 go test ./... && go build -trimpath -o /out/cull ./cmd/cull && go build -trimpath -o /out/scale ./cmd/scale

FROM debian:bookworm-slim
RUN groupadd -g 10001 cull && useradd -u 10001 -g cull -M cull
WORKDIR /app
COPY --from=go /out/cull /out/scale /app/
COPY --from=web /build/dist /app/web/dist
USER 10001:10001
EXPOSE 8830
ENTRYPOINT ["/app/cull"]
CMD ["-db","/state/scale.db","-listen","0.0.0.0:8830","-demo-network"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD ["/app/cull","-check"]
