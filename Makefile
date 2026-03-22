# meshd Makefile — build, deploy, operate
#
# Deploy config from .env (gitignored):
#   DEPLOY_HOST (default: chromabook)
#   DEPLOY_PORT (default: 2535)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X github.com/safety-quotient-lab/meshd/internal/server.Version=$(VERSION)"

REMOTE_BIN   := /home/kashif/platform/meshd
REMOTE_BACKUP := /home/kashif/platform/meshd-backup-$(shell date +%Y%m%d-%H%M)

# Load .env if present
-include .env
DEPLOY_HOST ?= chromabook
DEPLOY_PORT ?= 2535
SSH_CMD = ssh -p $(DEPLOY_PORT) $(DEPLOY_HOST)
SCP_CMD = scp -P $(DEPLOY_PORT)

.PHONY: build deploy deploy-transfer deploy-restart deploy-validate status clean help

# ── Build ─────────────────────────────────────────────────────
build:
	@echo "Building meshd $(VERSION) (linux/amd64 + darwin/arm64)..."
	@GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o meshd-linux ./cmd/meshd/
	@go build $(LDFLAGS) -o meshd ./cmd/meshd/
	@echo "  Linux:  ./meshd-linux ($$(du -h meshd-linux | cut -f1))"
	@echo "  Darwin: ./meshd ($$(du -h meshd | cut -f1))"

# ── Deploy ────────────────────────────────────────────────────
deploy: build deploy-transfer deploy-restart deploy-validate
	@$(SSH_CMD) "echo $(VERSION) > /home/kashif/platform/.meshd-version"
	@echo ""
	@echo "Deploy complete ($(VERSION))."

deploy-transfer:
	@echo ""
	@echo "═══ Transferring meshd binary ═══"
	@$(SCP_CMD) ./meshd-linux $(DEPLOY_HOST):$(REMOTE_BIN).new
	@echo "  Transferred to $(REMOTE_BIN).new"

deploy-restart:
	@echo ""
	@echo "═══ Restarting meshd ═══"
	@$(SSH_CMD) '\
		echo "  Stopping meshd-interagent-mesh..." && \
		systemctl --user kill -s SIGKILL meshd-interagent-mesh.service 2>/dev/null; \
		sleep 1 && \
		echo "  Swapping binary..." && \
		cp $(REMOTE_BIN) $(REMOTE_BACKUP) 2>/dev/null; \
		mv $(REMOTE_BIN).new $(REMOTE_BIN) && chmod +x $(REMOTE_BIN) && \
		echo "  Starting meshd-interagent-mesh..." && \
		systemctl --user start meshd-interagent-mesh.service && \
		sleep 3 && \
		echo "" && echo "  Process:" && \
		pgrep -f "/home/kashif/platform/meshd" -la 2>/dev/null | head -3'

deploy-validate:
	@echo ""
	@echo "═══ Post-deploy validation ═══"
	@sleep 5
	@curl -sf https://mesh.safety-quotient.dev/health && echo "  mesh.safety-quotient.dev: OK" || echo "  mesh.safety-quotient.dev: FAILED"
	@curl -sf https://psychology-agent.safety-quotient.dev/health && echo "  psychology-agent: OK" || echo "  psychology-agent: FAILED"
	@curl -sf https://psq-agent.safety-quotient.dev/health && echo "  psq-agent: OK" || echo "  psq-agent: FAILED"

# ── Operations ────────────────────────────────────────────────
status:
	@$(SSH_CMD) 'pgrep -f "platform/meshd --port" -la'

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
	@echo "  make build    Build linux/amd64 + darwin/arm64"
	@echo "  make deploy   Build + transfer + restart + validate"
	@echo "  make release  Build snapshot via goreleaser"
	@echo "  make status   Show running meshd processes on $(DEPLOY_HOST)"
	@echo "  make clean    Remove built binaries"
