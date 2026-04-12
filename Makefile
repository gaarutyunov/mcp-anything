.PHONY: build build-operator lint vet test integration e2e check clean helm-lint helm-package helm-push

BINARY := bin/proxy
OPERATOR_BINARY := bin/operator
GOFLAGS := -race
INTEGRATION_TIMEOUT := 600s

HELM_CHART_DIR := charts/mcp-anything
HELM_DIST_DIR := dist
HELM_REGISTRY ?= oci://ghcr.io/gaarutyunov

build:
	go build -o $(BINARY) ./cmd/proxy

build-operator:
	go build -o $(OPERATOR_BINARY) ./cmd/operator

lint:
	golangci-lint run ./...

vet:
	go vet ./...

test:
	go test $(GOFLAGS) -count=1 ./...

integration:
	go test $(GOFLAGS) -tags integration -count=1 -timeout $(INTEGRATION_TIMEOUT) ./tests/integration/...

e2e:
	go test $(GOFLAGS) -tags e2e -count=1 -timeout $(INTEGRATION_TIMEOUT) ./tests/e2e/...

check: lint vet test build build-operator

clean:
	rm -rf bin/

helm-lint:
	helm lint $(HELM_CHART_DIR)

helm-package:
	mkdir -p $(HELM_DIST_DIR)
	helm package $(HELM_CHART_DIR) --destination $(HELM_DIST_DIR)

helm-push: helm-package
	helm push $(HELM_DIST_DIR)/mcp-anything-*.tgz $(HELM_REGISTRY)
