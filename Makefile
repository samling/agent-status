BIN := bin/agent-status

.PHONY: build install bootstrap test clean

build:
	go build -o $(BIN) ./cmd/agent-status

install:
	go install ./cmd/agent-status

bootstrap:
	bash scripts/bootstrap.sh

test:
	go test ./...

clean:
	rm -rf bin dist
