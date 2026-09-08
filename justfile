# dragon-dash - development runs on the desktop, dragon is the deployment target.

app := "dragon-dash"

default:
    @just --list

# Run locally on http://127.0.0.1:9494
run *ARGS:
    go run ./cmd/{{app}} -addr 127.0.0.1:9494 {{ARGS}}

# Run and accept connections from the LAN (to view from another machine)
run-lan:
    go run ./cmd/{{app}} -addr 0.0.0.0:9494

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

# Cut a release: check that VERSION matches version.go and the README badge on a
# clean main, then tag vVERSION and push it. The tag triggers the Release
# workflow, which builds the archives and the deb/rpm/Arch packages.
release VERSION:
    #!/usr/bin/env bash
    set -euo pipefail
    ver="{{VERSION}}"
    ver="${ver#v}"
    grep -q "\"$ver\"" internal/version/version.go || { echo "internal/version/version.go is not at $ver"; exit 1; }
    grep -q "version-$ver-" README.md || { echo "the README badge is not at $ver"; exit 1; }
    branch="$(git rev-parse --abbrev-ref HEAD)"
    [ "$branch" = "main" ] || { echo "not on main (on $branch)"; exit 1; }
    [ -z "$(git status --porcelain)" ] || { echo "working tree is dirty; commit first"; exit 1; }
    git tag "v$ver"
    git push origin "v$ver"
    echo "pushed tag v$ver - watch the Release workflow for the published artifacts"

# Local dry run of the whole packaging pipeline: builds the binary and every
# distro package into ./dist without publishing (needs goreleaser on PATH)
release-snapshot:
    goreleaser release --snapshot --clean
