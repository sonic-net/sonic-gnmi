.PHONY: help help-test-tools proto build run clean test test-loopback test-coverage coverage-html coverage-summary validate-coverage test-e2e format validate-format vet tidy verify ci docker-build docker-push lint deb test-certs build-test-tools

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
DOCKER_IMAGE ?= gnmi-standalone-test

##############################################################################
# Help Target
##############################################################################

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-20s %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

help-test-tools: ## Show detailed help for test utilities
	@echo 'Test Utilities Help'
	@echo '==================='
	@echo ''
	@echo 'The upgrade service provides several test utilities for validation:'
	@echo ''
	@echo 'bootloader:'
	@echo '  Purpose: Test bootloader detection and image management functionality'
	@echo '  Usage:   ./bin/bootloader [--help]'
	@echo '  Safety:  Read-only, does not modify system configuration'
	@echo ''
	@echo 'installer:'
	@echo '  Purpose: Test sonic-installer CLI wrapper functionality'
	@echo '  Usage:   ./bin/installer <command> [args...]'
	@echo '  Safety:  list command is read-only, set-default/cleanup modify system'
	@echo '  Note:    Use with caution - some commands modify bootloader config'
	@echo ''
	@echo 'image-inspector:'
	@echo '  Purpose: Test image analysis and version extraction'
	@echo '  Usage:   ./bin/image-inspector [options] [image-file]'
	@echo '  Safety:  Read-only, analyzes image files without modification'
	@echo ''
	@echo 'redis:'
	@echo '  Purpose: Test Redis client for CONFIG_DB operations'
	@echo '  Usage:   ./bin/redis [--help]'
	@echo '  Safety:  Read-only, performs HGET operations on DEVICE_METADATA'
	@echo ''
	@echo 'download:'
	@echo '  Purpose: Test firmware download with sophisticated retry strategies'
	@echo '  Usage:   ./bin/download -url <URL> [options]'
	@echo '  Safety:  Downloads files to specified location, mimics curl behavior'
	@echo '  Note:    Supports interface binding, IPv4/IPv6, and fallback mechanisms'
	@echo ''
	@echo 'Building:'
	@echo '  make build-test-tools     # Build all test utilities'
	@echo '  make clean               # Remove built binaries'
	@echo ''
	@echo 'Note: These tools are for testing and validation only.'
	@echo '      They help verify that the upgrade service components'
	@echo '      work correctly on your system before deployment.'

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

test-loopback: ## Run loopback integration tests
	go test ./tests/loopback/... -v

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


# Generate test certificates for TLS testing
test-certs: ## Generate test certificates for TLS testing
	@echo "Generating test certificates..."
	@./scripts/generate-test-certs.sh
	@echo "Test certificates generated in current directory"

##############################################################################
# Build Targets
##############################################################################

# Build the server, sonic-gnmi CLI, and upgrade-agent
build: ## Build the sonic-gnmi-standalone, sonic-gnmi, and upgrade-agent binaries
	go build -tags "$(BUILD_TAGS)" -o bin/sonic-gnmi-standalone cmd/server/main.go
	go build -tags "$(BUILD_TAGS)" -o bin/sonic-gnmi cmd/sonic-gnmi/main.go
	go build -tags "$(BUILD_TAGS)" -o bin/upgrade-agent ./cmd/upgrade-agent/

# Build all packages
build-all: ## Build all packages (validate only)
	go build ./...

# Build all test utilities
build-test-tools: ## Build all test utilities
	@echo "Building test utilities..."
	@mkdir -p bin/
	@go build -o bin/bootloader cmd/test/bootloader/main.go
	@go build -o bin/installer cmd/test/installer/main.go
	@go build -o bin/image-inspector cmd/test/image-inspector/main.go
	@go build -o bin/redis cmd/test/redis/main.go
	@go build -o bin/diskspace cmd/test/diskspace/main.go
	@go build -o bin/download cmd/test/download/main.go
	@echo "Built test utilities:"
	@echo "  bin/bootloader       - Test bootloader detection and image management"
	@echo "  bin/installer        - Test sonic-installer CLI wrapper"
	@echo "  bin/image-inspector  - Test image analysis and version extraction"
	@echo "  bin/redis            - Test Redis client for CONFIG_DB operations"
	@echo "  bin/diskspace        - Test disk space analysis and monitoring"
	@echo "  bin/download         - Test firmware download with retry strategies"
	@echo ""
	@echo "Note: These are test utilities for validation purposes only."
	@echo "      Use 'make help-test-tools' for more information."

# Run the server
run: build ## Build and run the server
	./bin/sonic-gnmi-standalone

# Clean build artifacts
clean: ## Clean build artifacts
	rm -rf bin/

##############################################################################
# Security Analysis
##############################################################################

# Run semgrep security analysis
semgrep: ## Run semgrep security analysis
	@echo "Running semgrep security analysis..."
	@if command -v semgrep >/dev/null 2>&1; then \
		semgrep --config=p/default . --error; \
	else \
		echo "WARNING: semgrep not installed. Install with: pip install semgrep"; \
		echo "Skipping semgrep check..."; \
	fi

##############################################################################
# CI/CD Targets
##############################################################################

# CI pipeline target - runs all verification steps (validation only, no tool installation)
ci: validate-format tidy build-all vet lint test test-coverage verify ## Run full CI pipeline

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
	@if [ -f ../sonic-gnmi-standalone_*.deb ]; then \
		mv ../sonic-gnmi-standalone_*.* build/ 2>/dev/null || true; \
	fi
	@echo "Debian package built in build/ directory"
