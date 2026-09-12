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
    go build -trimpath -ldflags="-s -w" -o /armdash ./cmd/armdash

FROM scratch
COPY --from=build /armdash /armdash
# Needed only if a future system talks to an HTTPS endpoint directly.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
ENTRYPOINT ["/armdash", "-addr", "0.0.0.0:8080"]
