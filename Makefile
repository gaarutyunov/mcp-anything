.PHONY: all build build-operator lint vet test integration treeshake check clean helm-lint helm-package helm-push generate-crds lint-crds

BINARY := bin/proxy
OPERATOR_BINARY := bin/operator
GOFLAGS := -race
INTEGRATION_TIMEOUT := 600s
E2E_TEST ?=
E2E_RUN_FLAG = $(if $(E2E_TEST),-run $(E2E_TEST),)

HELM_CHART_DIR := charts/mcp-anything
HELM_DIST_DIR := dist
HELM_REGISTRY ?= oci://ghcr.io/gaarutyunov

all: check

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
	go test $(GOFLAGS) -tags e2e -count=1 -timeout $(INTEGRATION_TIMEOUT) $(E2E_RUN_FLAG) ./tests/e2e/...

treeshake:
	go test -tags treeshake -count=1 ./tests/treeshake/...

generate-crds:
	go run ./cmd/crdgen

lint-crds:
	go run ./cmd/crdlint

check: lint vet test build build-operator treeshake lint-crds

clean:
	rm -rf bin/

helm-lint:
	helm lint $(HELM_CHART_DIR)

helm-package:
	mkdir -p $(HELM_DIST_DIR)
	helm package $(HELM_CHART_DIR) --destination $(HELM_DIST_DIR)

helm-push: helm-package
	helm push $(HELM_DIST_DIR)/mcp-anything-*.tgz $(HELM_REGISTRY)
