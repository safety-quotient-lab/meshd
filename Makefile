# meshd Makefile — build, deploy, operate
#
# Local deploy (gray-box) — launchd service dev.safety-quotient.meshd
# Binary built in-place; launchd restarts from the same path.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME := $(shell date +%s)
LDFLAGS := -ldflags "-X github.com/safety-quotient-lab/meshd/internal/server.Version=$(VERSION) -X github.com/safety-quotient-lab/meshd/internal/server.BuildTime=$(BUILD_TIME)"
SERVICE := dev.safety-quotient.meshd

.PHONY: build deploy deploy-restart deploy-validate status clean help

# ── Build ─────────────────────────────────────────────────────
build:
	@echo "Building meshd $(VERSION) (darwin/arm64 + linux/amd64)..."
	@go build $(LDFLAGS) -o meshd ./cmd/meshd/
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o meshd-linux ./cmd/meshd/
	@echo "  Darwin: ./meshd ($$(du -h meshd | cut -f1))"
	@echo "  Linux:  ./meshd-linux ($$(du -h meshd-linux | cut -f1))"

# ── Deploy (local — launchd service) ─────────────────────────
deploy: build deploy-restart deploy-validate
	@echo ""
	@echo "Deploy complete ($(VERSION))."

deploy-restart:
	@echo ""
	@echo "═══ Restarting $(SERVICE) ═══"
	@launchctl stop $(SERVICE) 2>/dev/null; sleep 1
	@echo "  Service stopped"
	@launchctl start $(SERVICE)
	@sleep 2
	@echo "  Service started (PID $$(pgrep -f 'meshd.*--port 8081' | head -1))"

deploy-validate:
	@echo ""
	@echo "═══ Post-deploy validation ═══"
	@sleep 8
	@curl -sf http://localhost:8081/health && echo "  localhost:8081 OK" || echo "  localhost:8081 FAILED"
	@curl -sf https://mesh.safety-quotient.dev/health && echo "  mesh.safety-quotient.dev OK" || echo "  mesh.safety-quotient.dev FAILED"

# ── Operations ────────────────────────────────────────────────
status:
	@pgrep -lf "meshd.*--port 8081" || echo "meshd not running"
	@echo "---"
	@launchctl list $(SERVICE) 2>/dev/null || echo "service not loaded"

logs:
	@launchctl print user/$$(id -u)/$(SERVICE) 2>/dev/null | head -20

# ── Release (goreleaser) ─────────────────────────────────────
release:
	goreleaser release --snapshot --clean

# ── Housekeeping ─────────────────────────────────────────────
clean:
	@rm -f meshd meshd-linux meshd-darwin meshd-linux-amd64
	@rm -rf dist/

help:
	@echo "meshd Makefile ($(VERSION))"
	@echo ""
	@echo "  make build    Build darwin/arm64 + linux/amd64"
	@echo "  make deploy   Build + restart launchd service + validate"
	@echo "  make status   Show running meshd process + service state"
	@echo "  make logs     Show launchd service info"
	@echo "  make release  Build snapshot via goreleaser"
	@echo "  make clean    Remove built binaries"
