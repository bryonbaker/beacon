# Beacon Makefile

REGISTRY ?= quay.io
REPOSITORY ?= bryonbaker
IMAGE_NAME ?= beacon
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo "dev")
IMAGE_TAG ?= $(REGISTRY)/$(REPOSITORY)/$(IMAGE_NAME):$(VERSION)
IMAGE_LATEST ?= $(REGISTRY)/$(REPOSITORY)/$(IMAGE_NAME):latest

BINARY_NAME = beacon
BUILD_DIR = ./cmd/beacon
CGO_ENABLED = 1

.PHONY: help deps fmt vet lint test test-coverage build build-linux clean \
        image-build image-push image-build-push \
        deploy deploy-manifests deploy-dev deploy-prod undeploy \
        logs port-forward-metrics db-shell db-query version ci release

help: ## Show this help message
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-25s %s\n", $$1, $$2}'

deps: ## Download Go module dependencies
	CGO_ENABLED=$(CGO_ENABLED) go mod download
	CGO_ENABLED=$(CGO_ENABLED) go mod tidy

fmt: ## Format Go source files
	go fmt ./...

vet: ## Run go vet
	CGO_ENABLED=$(CGO_ENABLED) go vet ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

test: ## Run tests
	CGO_ENABLED=$(CGO_ENABLED) go test -race -v ./...

test-coverage: ## Run tests with coverage report
	CGO_ENABLED=$(CGO_ENABLED) go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

build: ## Build the binary for the current platform
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="-s -w" -o $(BINARY_NAME) $(BUILD_DIR)

build-linux: ## Build the binary for Linux amd64
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BINARY_NAME) $(BUILD_DIR)

clean: ## Remove build artifacts
	rm -f $(BINARY_NAME)
	rm -f coverage.out coverage.html

image-build: ## Build the container image with podman
	podman build -t $(IMAGE_TAG) -t $(IMAGE_LATEST) -f Containerfile .

image-push: ## Push the container image with podman
	podman push $(IMAGE_TAG)
	podman push $(IMAGE_LATEST)

image-build-push: image-build image-push ## Build and push the container image

deploy-manifests: ## Apply all Kubernetes manifests
	kubectl apply -f deployments/namespace.yaml
	kubectl apply -f deployments/serviceaccount.yaml
	kubectl apply -f deployments/clusterrole.yaml
	kubectl apply -f deployments/clusterrolebinding.yaml
	kubectl apply -f deployments/configmap.yaml
	kubectl apply -f deployments/secret.yaml
	kubectl apply -f deployments/pvc.yaml
	kubectl apply -f deployments/deployment.yaml
	kubectl apply -f deployments/service.yaml
	kubectl apply -f deployments/servicemonitor.yaml

deploy: deploy-dev ## Deploy to Kubernetes (alias for deploy-dev)

deploy-dev: ## Deploy to dev environment
	@echo "Deploying to dev environment..."
	$(MAKE) deploy-manifests

deploy-prod: ## Deploy to prod environment (with confirmation)
	@echo "WARNING: You are about to deploy to PRODUCTION."
	@read -p "Are you sure? [y/N] " confirm && [ "$$confirm" = "y" ] || (echo "Aborted." && exit 1)
	@echo "Deploying to production..."
	$(MAKE) deploy-manifests

undeploy: ## Remove all Kubernetes resources
	kubectl delete -f deployments/ --ignore-not-found

logs: ## Tail logs from the beacon pod
	kubectl logs -f -n beacon -l app=beacon

port-forward-metrics: ## Port-forward metrics endpoint to localhost:8080
	kubectl port-forward -n beacon svc/beacon 8080:8080

db-shell: ## Open a shell to the database file in the pod
	kubectl exec -it -n beacon $$(kubectl get pod -n beacon -l app=beacon -o jsonpath='{.items[0].metadata.name}') -- /bin/sh

db-query: ## Run a SQL query against the database (usage: make db-query SQL="SELECT * FROM managed_objects LIMIT 10")
	kubectl exec -it -n beacon $$(kubectl get pod -n beacon -l app=beacon -o jsonpath='{.items[0].metadata.name}') -- sqlite3 /data/events.db "$(SQL)"

version: ## Print the current version
	@echo "$(VERSION)"

ci: deps fmt vet lint test ## Run all CI checks

release: ci image-build-push ## Run CI checks then build and push the image
	@echo "Released $(IMAGE_TAG)"
	@echo "Released $(IMAGE_LATEST)"
