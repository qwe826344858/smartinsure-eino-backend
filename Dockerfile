# syntax=docker/dockerfile:1

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src

ARG TARGETOS=linux
ARG TARGETARCH=amd64

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY configs ./configs

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/ingesturls ./cmd/ingesturls
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/exportplatformproducts ./cmd/exportplatformproducts

FROM alpine:3.22
WORKDIR /app

RUN addgroup -S app && adduser -S -G app app && mkdir -p /app/logs && chown -R app:app /app/logs

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/server /app/server
COPY --from=build /out/ingesturls /app/ingesturls
COPY --from=build /out/exportplatformproducts /app/exportplatformproducts
COPY configs ./configs

ENV APP_ENV=production \
    HTTP_ADDR=0.0.0.0:34567 \
    ORCHESTRATOR=lite

EXPOSE 34567

USER app

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:34567/api/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/server"]
