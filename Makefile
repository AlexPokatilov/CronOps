GOBIN ?= $(shell go env GOPATH)/bin
CONTROLLER_GEN = $(GOBIN)/controller-gen
IMAGE_PREFIX ?= ghcr.io/alexpokatilov
TAG ?= latest

.PHONY: all
all: generate build test

.PHONY: build
build: ## Build both binaries
	go build ./...

.PHONY: test
test: ## Run Go unit tests
	go test ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: generate
generate: $(CONTROLLER_GEN) ## Regenerate deepcopy code and the CRD manifest
	$(CONTROLLER_GEN) object paths=./api/...
	$(CONTROLLER_GEN) crd paths=./... output:crd:artifacts:config=deploy/crd
	cp deploy/crd/*.yaml deploy/chart/crds/

$(CONTROLLER_GEN):
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

.PHONY: web
web: ## Build the web UI
	cd web && npm ci && npm run build

.PHONY: docker
docker: ## Build container images
	docker build -f Dockerfile.controller -t $(IMAGE_PREFIX)/cronops-controller:$(TAG) .
	docker build -f Dockerfile.server -t $(IMAGE_PREFIX)/cronops-server:$(TAG) .

.PHONY: kind-up
kind-up: ## Create a local kind cluster with the CRD installed
	hack/kind-up.sh

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
