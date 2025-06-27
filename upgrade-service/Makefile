.PHONY: help proto build run clean test test-coverage coverage-html coverage-summary validate-coverage test-e2e format validate-format vet tidy verify ci docker-build docker-push lint deb test-certs build-inspector

# Default target shows help
.DEFAULT_GOAL := help

# Include tool management makefile
include tools.mk

##############################################################################
# Configuration Variables
##############################################################################

# Default go build tags
BUILD_TAGS ?=
COVERAGE_THRESHOLD ?= 25
DOCKER_TAG ?= latest
DOCKER_IMAGE ?= opsd

##############################################################################
# Help Target
##############################################################################

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

##############################################################################
# Code Formatting & Validation
##############################################################################


# Fix formatting with gofmt
format: ## Fix code formatting with gofmt
	@echo "Fixing formatting (gofmt)..."
	@gofmt -w .
	@echo "Formatting fixed."

# Validate formatting with gofmt (for CI)
validate-format: ## Validate code formatting (for CI)
	@echo "Validating formatting (gofmt)..."
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "ERROR: gofmt issues found in the following files:"; \
		echo "$$UNFORMATTED"; \
		gofmt -d .; \
		exit 1; \
	fi


##############################################################################
# Protocol Buffers
##############################################################################

# Generate protobuf files
proto: validate-protobuf-tools ## Generate protobuf files
	PATH="$(shell go env GOPATH)/bin:$$PATH" protoc --go_out=. --go_opt=paths=source_relative \
        --go-grpc_out=. --go-grpc_opt=paths=source_relative \
        proto/*.proto

# Validate proto-generated files are up to date
validate-proto: validate-protobuf-tools ## Validate proto-generated files are up to date
	@echo "Validating proto-generated files..."
	@TEMP_DIR=$$(mktemp -d); \
	PATH="$(shell go env GOPATH)/bin:$$PATH" protoc --go_out=$$TEMP_DIR --go_opt=paths=source_relative \
		--go-grpc_out=$$TEMP_DIR --go-grpc_opt=paths=source_relative \
		proto/*.proto; \
	mkdir -p $$TEMP_DIR/proto; \
	cp -r $$TEMP_DIR/*.pb.go $$TEMP_DIR/proto/ 2>/dev/null || true; \
	DIFF_FILES=false; \
	for file in $$(find proto/ -name "*.pb.go" 2>/dev/null); do \
		base_file=$$(basename "$$file"); \
		if [ ! -f "$$TEMP_DIR/proto/$$base_file" ]; then \
			echo "Generated file $$base_file is missing from temporary directory"; \
			DIFF_FILES=true; \
		elif ! diff -u "$$file" "$$TEMP_DIR/proto/$$base_file" > /dev/null; then \
			echo "Differences found in $$base_file:"; \
			diff -u "$$file" "$$TEMP_DIR/proto/$$base_file"; \
			DIFF_FILES=true; \
		fi; \
	done; \
	for file in $$(find $$TEMP_DIR/proto/ -name "*.pb.go" 2>/dev/null); do \
		base_file=$$(basename "$$file"); \
		if [ ! -f "proto/$$base_file" ]; then \
			echo "Generated file $$base_file is missing from committed files"; \
			DIFF_FILES=true; \
		fi; \
	done; \
	if [ "$$DIFF_FILES" = true ]; then \
		echo "ERROR: Proto-generated files are not up to date. Please regenerate them locally and commit the changes."; \
		echo "Run 'make proto' and commit the changes."; \
		rm -rf $$TEMP_DIR; \
		exit 1; \
	else \
		echo "Proto-generated files are up to date."; \
	fi; \
	rm -rf $$TEMP_DIR

##############################################################################
# Go Module Management
##############################################################################

# Tidy modules - cleans up unused dependencies in go.mod and go.sum
tidy: ## Clean up unused dependencies in go.mod and go.sum
	go mod tidy

# Verify modules
verify: ## Verify go modules
	go mod verify

##############################################################################
# Code Analysis
##############################################################################

# Run static analysis
vet: ## Run go vet static analysis
	go vet ./...

# Run golangci-lint
lint: validate-golangci-lint ## Run golangci-lint
	golangci-lint run

##############################################################################
# Testing
##############################################################################

# Run all tests
test: ## Run all tests
	go test ./... -timeout 2m

# Run all tests with coverage
test-coverage: ## Run all tests with coverage report
	@echo "Running tests with coverage..."
	@go test ./... -timeout 2m -coverprofile=coverage.out -covermode=atomic
	@echo "Coverage report generated: coverage.out"

# Generate HTML coverage report
coverage-html: test-coverage ## Generate HTML coverage report
	@echo "Generating HTML coverage report..."
	@go tool cover -html=coverage.out -o coverage.html
	@echo "HTML coverage report generated: coverage.html"

# Show coverage summary
coverage-summary: test-coverage ## Show coverage summary
	@echo "Coverage summary:"
	@go tool cover -func=coverage.out

# Validate coverage meets minimum threshold
validate-coverage: test-coverage ## Validate coverage meets minimum threshold
	@echo "Validating coverage meets minimum threshold..."
	@COVERAGE=$$(go tool cover -func=coverage.out | grep total | awk '{print $$3}' | sed 's/%//'); \
	THRESHOLD=$(COVERAGE_THRESHOLD); \
	if [ "$$(echo "$$COVERAGE < $$THRESHOLD" | bc -l)" -eq 1 ]; then \
		echo "ERROR: Coverage $$COVERAGE% is below minimum threshold $$THRESHOLD%"; \
		exit 1; \
	else \
		echo "✓ Coverage $$COVERAGE% meets minimum threshold $$THRESHOLD%"; \
	fi

# Run end-to-end tests
test-e2e: ## Run end-to-end tests
	go test ./tests/e2e/... -timeout 2m

# Generate test certificates for TLS testing
test-certs: ## Generate test certificates for TLS testing
	@echo "Generating test certificates..."
	@./scripts/generate-test-certs.sh
	@echo "Test certificates generated in current directory"

##############################################################################
# Build Targets
##############################################################################

# Build the server
build: validate-proto ## Build the sonic-ops-server binary
	go build -tags "$(BUILD_TAGS)" -o bin/sonic-ops-server cmd/server/main.go

# Build all packages
build-all: ## Build all packages (validate only)
	go build ./...

# Build sonic-image-inspector tool for manual testing
build-inspector: ## Build the sonic-image-inspector tool
	@echo "Building sonic-image-inspector tool..."
	@go build -o bin/sonic-image-inspector cmd/sonic-image-inspector/main.go
	@echo "Built bin/sonic-image-inspector"

# Run the server
run: build ## Build and run the server
	./bin/sonic-ops-server

# Clean build artifacts
clean: ## Clean build artifacts
	rm -rf bin/

##############################################################################
# CI/CD Targets
##############################################################################

# CI pipeline target - runs all verification steps (validation only, no tool installation)
ci: validate-format validate-proto tidy build-all vet lint test validate-coverage test-e2e verify ## Run full CI pipeline

##############################################################################
# Packaging & Distribution
##############################################################################

# Build Docker image
docker-build: build ## Build Docker image
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -f docker/Dockerfile .

# Push Docker image to registry
docker-push: docker-build ## Push Docker image to registry
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

# Build debian package
deb: build ## Build debian package
	@echo "Building debian package..."
	@mkdir -p build
	@rm -rf build/*
	@dpkg-buildpackage -us -uc -d --build-dir=build 2>/dev/null || dpkg-buildpackage -us -uc -d
	@if [ -f ../sonic-ops-server_*.deb ]; then \
		mv ../sonic-ops-server_*.* build/ 2>/dev/null || true; \
	fi
	@echo "Debian package built in build/ directory"
