BIN := bin/agent-status

.PHONY: build
build:
	go build -o $(BIN) ./cmd/agent-status
