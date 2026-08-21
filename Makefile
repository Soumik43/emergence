.PHONY: build test lint run clean

BIN := bin/emergence
QUERY ?= AI agents for SMBs

build:
	go build -o $(BIN) ./cmd/emergence

# Needs no API key and makes no network calls.
test:
	go test ./...

lint:
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...

# make run QUERY="AI bookkeeping for restaurants"
run: build
	./$(BIN) run --query "$(QUERY)"

clean:
	rm -rf bin
