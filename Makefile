BIN := bin/agent-status

SYSTEMD_USER_DIR := $(HOME)/.config/systemd/user

.PHONY: build install bootstrap install-service test clean

build:
	go build -o $(BIN) ./cmd/agent-status

install:
	go install ./cmd/agent-status

bootstrap:
	bash scripts/bootstrap.sh

install-service:
	mkdir -p $(SYSTEMD_USER_DIR)
	install -m 0644 contrib/systemd/user/agent-status.service $(SYSTEMD_USER_DIR)/agent-status.service
	systemctl --user daemon-reload
	@echo "installed to $(SYSTEMD_USER_DIR)/agent-status.service"
	@echo "enable with: systemctl --user enable --now agent-status"

test:
	go test ./...

clean:
	rm -rf bin dist
