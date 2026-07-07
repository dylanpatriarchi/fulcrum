BINARY   := fulcrum
PKG      := ./...
BIN_DIR  := bin
CONFIG   ?= config.example.yaml

.PHONY: all build run test test-race vet lint fmt tidy cover docker clean loadtest demo

all: vet test-race build

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/fulcrum
	go build -o $(BIN_DIR)/loadgen ./cmd/loadgen

run: build
	./$(BIN_DIR)/$(BINARY) -config $(CONFIG)

test:
	go test $(PKG)

test-race:
	go test -race -count=1 $(PKG)

vet:
	go vet $(PKG)

lint:
	golangci-lint run

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

cover:
	go test -race -coverprofile=coverage.txt -covermode=atomic $(PKG)
	go tool cover -func=coverage.txt | tail -1

loadtest:
	./scripts/loadtest.sh

demo:
	./scripts/failover-demo.sh

docker:
	docker build -t $(BINARY):latest .

clean:
	rm -rf $(BIN_DIR) coverage.txt coverage.html
