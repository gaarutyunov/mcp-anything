.PHONY: all build build-operator lint vet test integration treeshake check clean helm-lint helm-package helm-push generate-crds lint-crds build-linter cover-merge cover-report

BINARY := bin/proxy
OPERATOR_BINARY := bin/operator
LINTER_BINARY := bin/golangci-lint-custom
GOFLAGS := -race
INTEGRATION_TIMEOUT := 600s
E2E_TEST ?=
E2E_RUN_FLAG = $(if $(E2E_TEST),-run $(E2E_TEST),)
COVERAGE_DIR ?= $(CURDIR)/coverage

HELM_CHART_DIR := charts/mcp-anything
HELM_DIST_DIR := dist
HELM_REGISTRY ?= oci://ghcr.io/gaarutyunov

all: check

build:
	go build -o $(BINARY) ./cmd/proxy

build-operator:
	go build -o $(OPERATOR_BINARY) ./cmd/operator

build-linter:
	go build -o $(LINTER_BINARY) ./cmd/golangci-lint-custom

lint: build-linter
	$(LINTER_BINARY) run ./...

vet:
	go vet ./...

test:
	mkdir -p $(COVERAGE_DIR)/unit
	GOCOVERDIR=$(COVERAGE_DIR)/unit go test $(GOFLAGS) -cover -coverpkg=./... -count=1 ./...

integration:
	mkdir -p $(COVERAGE_DIR)/integration
	GOCOVERDIR=$(COVERAGE_DIR)/integration COVERAGE_DIR=$(COVERAGE_DIR)/integration go test $(GOFLAGS) -tags integration -cover -coverpkg=./... -count=1 -timeout $(INTEGRATION_TIMEOUT) ./tests/integration/...

e2e:
	go test $(GOFLAGS) -tags e2e -count=1 -timeout $(INTEGRATION_TIMEOUT) $(E2E_RUN_FLAG) ./tests/e2e/...

treeshake:
	go test -tags treeshake -count=1 ./tests/treeshake/...

generate-crds:
	go run ./cmd/crdgen

lint-crds:
	go run ./cmd/crdlint

check: lint vet test build build-operator treeshake

cover-merge:
	mkdir -p $(COVERAGE_DIR)/merged
	go tool covdata merge -i $(COVERAGE_DIR)/unit,$(COVERAGE_DIR)/integration -o $(COVERAGE_DIR)/merged
	go tool covdata textfmt -i $(COVERAGE_DIR)/merged -o $(COVERAGE_DIR)/coverage.out

cover-report: cover-merge
	go tool cover -func=$(COVERAGE_DIR)/coverage.out

clean:
	rm -rf bin/

helm-lint:
	helm lint $(HELM_CHART_DIR)

helm-package:
	mkdir -p $(HELM_DIST_DIR)
	helm package $(HELM_CHART_DIR) --destination $(HELM_DIST_DIR)

helm-push: helm-package
	helm push $(HELM_DIST_DIR)/mcp-anything-*.tgz $(HELM_REGISTRY)
