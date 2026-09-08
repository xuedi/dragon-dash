# dragon-dash - development runs on the desktop; dragon comes later.

app := "dragon-dash"

default:
    @just --list

# Run locally on http://127.0.0.1:8080
run *ARGS:
    go run ./cmd/{{app}} -addr 127.0.0.1:8080 {{ARGS}}

# Run and accept connections from the LAN (to view from another machine)
run-lan:
    go run ./cmd/{{app}} -addr 0.0.0.0:8080

build:
    go build -trimpath -ldflags="-s -w" -o bin/{{app}} ./cmd/{{app}}

# The binary that goes to dragon. No cgo, so no cross toolchain needed.
build-arm:
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
      go build -trimpath -ldflags="-s -w" -o bin/{{app}}-arm64 ./cmd/{{app}}

check:
    gofmt -l ./cmd ./internal ./web
    go vet ./...
    go test ./...

# Prometheus + node_exporter on the desktop, so the Dragon pages have real data
# while developing. Point Settings at http://127.0.0.1:9090
dev-up:
    docker compose -f deploy/docker-compose.dev.yml up -d

dev-down:
    docker compose -f deploy/docker-compose.dev.yml down

# Copy the arm64 binary to dragon (does not install or start anything)
push-arm: build-arm
    scp bin/{{app}}-arm64 dragon:/tmp/{{app}}
