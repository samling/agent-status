BIN := bin/agent-status

SYSTEMD_USER_DIR := $(HOME)/.config/systemd/user

# VERSION resolves to the closest git tag (with a "-dirty" suffix when
# the working tree has uncommitted changes), or "dev" outside a git
# checkout. Override with `make build VERSION=...`.
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X github.com/samling/agent-status/internal/version.Version=$(VERSION)

.PHONY: build install bootstrap install-service test check check-cross clean

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/agent-status

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/agent-status

bootstrap: build
	$(BIN) bootstrap

install-service:
	mkdir -p $(SYSTEMD_USER_DIR)
	install -m 0644 contrib/systemd/user/agent-status.service $(SYSTEMD_USER_DIR)/agent-status.service
	systemctl --user daemon-reload
	@echo "installed to $(SYSTEMD_USER_DIR)/agent-status.service"
	@echo "enable with: systemctl --user enable --now agent-status"

test:
	go test ./...

# check runs vet + tests on the host platform.
check:
	go vet ./...
	go test ./...

# check-cross verifies every OS we ship build tags for compiles cleanly.
# Useful when touching internal/focus/ or anything else with //go:build.
check-cross:
	@for os in linux darwin freebsd; do \
	  echo "==> GOOS=$$os"; \
	  GOOS=$$os go build ./... || exit 1; \
	done

clean:
	rm -rf bin dist
