# Beacon Test Endpoint Makefile

REGISTRY ?= quay.io
REPOSITORY ?= bryonbaker
IMAGE_NAME ?= beacon-test-endpoint
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
IMAGE_TAG ?= $(REGISTRY)/$(REPOSITORY)/$(IMAGE_NAME):$(VERSION)
IMAGE_LATEST ?= $(REGISTRY)/$(REPOSITORY)/$(IMAGE_NAME):latest

BINARY_NAME = beacon-test-endpoint
BUILD_DIR = ./cmd/test-endpoint

.PHONY: help deps fmt vet test build build-linux clean \
        image-build image-push image-build-push \
        deploy undeploy logs

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-25s %s\n", $$1, $$2}'

deps: ## Download Go module dependencies
	go mod download
	go mod tidy

fmt: ## Format Go source files
	go fmt ./...

vet: ## Run go vet
	go vet ./...

test: ## Run tests
	go test -race -v ./...

build: ## Build the binary for the current platform
	go build -ldflags="-s -w" -o $(BINARY_NAME) $(BUILD_DIR)

build-linux: ## Build the binary for Linux amd64
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY_NAME) $(BUILD_DIR)

clean: ## Remove build artifacts
	rm -f $(BINARY_NAME)

image-build: ## Build the container image with podman
	podman build -t $(IMAGE_TAG) -t $(IMAGE_LATEST) -f Containerfile .

image-push: ## Push the container image with podman
	podman push $(IMAGE_TAG)
	podman push $(IMAGE_LATEST)

image-build-push: image-build image-push ## Build and push the container image

deploy: ## Deploy to Kubernetes
	kubectl apply -f deployments/configmap.yaml
	kubectl apply -f deployments/deployment.yaml
	kubectl apply -f deployments/service.yaml

undeploy: ## Remove all Kubernetes resources
	kubectl delete -f deployments/ --ignore-not-found

logs: ## Tail logs from the test-endpoint pod
	kubectl logs -f -n beacon -l app=beacon-test-endpoint

version: ## Print the current version
	@echo "$(VERSION)"
