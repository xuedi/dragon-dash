# Static build: CGO is off and every dependency is the standard library, so the
# result runs on scratch and cross-compiles to arm64 without a toolchain.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-arm64} \
    go build -trimpath -ldflags="-s -w" -o /dragon-dash ./cmd/dragon-dash

FROM scratch
COPY --from=build /dragon-dash /dragon-dash
# Needed only if a future system talks to an HTTPS endpoint directly.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
ENTRYPOINT ["/dragon-dash", "-addr", "0.0.0.0:8080", "-config", "/data/config.json"]
