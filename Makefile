.PHONY: build lint vet test integration check clean

BINARY := bin/proxy
GOFLAGS := -race
INTEGRATION_TIMEOUT := 600s

build:
	go build -o $(BINARY) ./cmd/proxy

lint:
	golangci-lint run ./...

vet:
	go vet ./...

test:
	go test $(GOFLAGS) -count=1 ./...

integration:
	go test $(GOFLAGS) -tags integration -count=1 -timeout $(INTEGRATION_TIMEOUT) ./tests/integration/...

check: lint vet test build

clean:
	rm -rf bin/
