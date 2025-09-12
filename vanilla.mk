# vanilla.mk - Simple CI for vanilla-runnable packages without SONiC dependencies
# Usage: make -f vanilla.mk ci
#
# This makefile supports testing packages that don't require CGO or SONiC dependencies.
# Add new vanilla-runnable packages to VANILLA_PACKAGES below.
#
# Goal: Eventually all packages should be vanilla-runnable unless they absolutely
# require CGO dependencies. All CGO/SONiC dependencies should be properly quarantined.

# Go configuration
GO ?= go
GOROOT ?= $(shell $(GO) env GOROOT)

# Vanilla-runnable packages (no CGO/SONiC dependencies)
# Add new packages here as they become vanilla-compatible
VANILLA_PACKAGES := \
	internal/diskspace \
	pkg/server/upgrade-handler

# Future packages to make vanilla-runnable:
# TODO: sonic-gnmi-standalone/pkg/workflow
# TODO: sonic-gnmi-standalone/pkg/client/config  
# TODO: sonic-gnmi-standalone/internal/checksum
# TODO: sonic-gnmi-standalone/internal/download
# TODO: common_utils (parts that don't need CGO)
# TODO: gnoi_client/config
# TODO: transl_utils (isolate from translib dependencies)

# You can test specific packages by setting PACKAGES=pkg/specific/package
PACKAGES ?= $(VANILLA_PACKAGES)

# Default target
.DEFAULT_GOAL := ci

# Clean up any build artifacts
.PHONY: clean
clean:
	@echo "Cleaning vanilla build artifacts..."
	$(GO) clean -cache -testcache
	@for pkg in $(PACKAGES); do \
		rm -f $$pkg/coverage.out $$pkg/coverage.html; \
	done

# Format check - ensure code is properly formatted
.PHONY: fmt-check
fmt-check:
	@echo "Checking Go code formatting for vanilla packages..."
	@for pkg in $(PACKAGES); do \
		echo "Checking $$pkg..."; \
		files=$$($(GOROOT)/bin/gofmt -l $$pkg/*.go 2>/dev/null || true); \
		if [ -n "$$files" ]; then \
			echo "The following files need formatting in $$pkg:"; \
			echo "$$files"; \
			echo "Please run 'make -f vanilla.mk fmt' or 'gofmt -w $$pkg/*.go'"; \
			exit 1; \
		fi; \
	done
	@echo "All files are properly formatted."

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting Go code for vanilla packages..."
	@for pkg in $(PACKAGES); do \
		echo "Formatting $$pkg..."; \
		$(GOROOT)/bin/gofmt -w $$pkg/*.go 2>/dev/null || true; \
	done

# Vet - static analysis
.PHONY: vet
vet:
	@echo "Running go vet on vanilla packages..."
	@for pkg in $(PACKAGES); do \
		echo "Vetting $$pkg..."; \
		cd $$pkg && $(GO) vet ./...; \
		cd - >/dev/null; \
	done

# Test - run all tests with coverage
.PHONY: test
test:
	@echo "Running tests for vanilla packages..."
	@for pkg in $(PACKAGES); do \
		echo ""; \
		echo "=== Testing $$pkg ==="; \
		cd $$pkg && $(GO) test -v -race -coverprofile=coverage.out -covermode=atomic ./...; \
		if [ -f coverage.out ]; then \
			echo "Coverage for $$pkg:"; \
			$(GO) tool cover -func=coverage.out; \
		fi; \
		cd - >/dev/null; \
	done

# Test with coverage report
.PHONY: test-coverage
test-coverage: test
	@echo "Generating HTML coverage reports..."
	@for pkg in $(PACKAGES); do \
		if [ -f $$pkg/coverage.out ]; then \
			echo "Generating coverage report for $$pkg..."; \
			cd $$pkg && $(GO) tool cover -html=coverage.out -o coverage.html; \
			echo "Coverage report generated: $$pkg/coverage.html"; \
			cd - >/dev/null; \
		fi; \
	done

# Build test - ensure the package builds
.PHONY: build-test
build-test:
	@echo "Testing build of vanilla packages..."
	@for pkg in $(PACKAGES); do \
		echo "Building $$pkg..."; \
		cd $$pkg && $(GO) build -v ./...; \
		cd - >/dev/null; \
	done

# Lint check using basic go tools
.PHONY: lint
lint: fmt-check vet
	@echo "Basic linting complete for vanilla packages"

# Benchmark tests
.PHONY: bench
bench:
	@echo "Running benchmarks for vanilla packages..."
	@for pkg in $(PACKAGES); do \
		echo "Benchmarking $$pkg..."; \
		cd $$pkg && $(GO) test -bench=. -benchmem ./...; \
		cd - >/dev/null; \
	done

# Module verification
.PHONY: mod-verify
mod-verify:
	@echo "Verifying go modules..."
	$(GO) mod verify
	@echo "Checking if go.mod needs tidying..."
	@if ! $(GO) mod tidy -diff >/dev/null 2>&1; then \
		echo "Warning: go.mod could be tidied, but continuing CI..."; \
	else \
		echo "Go modules are clean"; \
	fi

# Security scan using gosec if available
.PHONY: security
security:
	@echo "Running security scan on vanilla packages..."
	@if command -v gosec >/dev/null 2>&1; then \
		for pkg in $(PACKAGES); do \
			echo "Scanning $$pkg..."; \
			cd $$pkg && gosec ./...; \
			cd - >/dev/null; \
		done; \
	else \
		echo "gosec not available, skipping security scan"; \
		echo "Install with: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; \
	fi

# List vanilla packages
.PHONY: list-packages
list-packages:
	@echo "Vanilla-runnable packages:"
	@for pkg in $(VANILLA_PACKAGES); do \
		echo "  - $$pkg"; \
	done
	@echo ""
	@echo "Currently testing packages:"
	@for pkg in $(PACKAGES); do \
		echo "  - $$pkg"; \
	done


# Full CI pipeline
.PHONY: ci
ci: clean mod-verify lint build-test test
	@echo ""
	@echo "============================================="
	@echo "✅ Vanilla CI completed successfully!"
	@echo "============================================="
	@echo "Tested packages:"
	@for pkg in $(PACKAGES); do \
		echo "  - $$pkg"; \
	done
	@echo ""
	@echo "Components validated:"
	@echo "  - Code formatting"
	@echo "  - Static analysis (vet)"  
	@echo "  - Build verification"
	@echo "  - Unit tests with race detection"
	@echo "  - Test coverage analysis"
	@echo ""
	@echo "This validates that these packages can be developed"
	@echo "and tested without SONiC/CGO dependencies."

# Quick check for development
.PHONY: quick
quick: fmt-check vet build-test
	@echo "Quick validation complete for vanilla packages"

# Help target
.PHONY: help
help:
	@echo "Vanilla CI Makefile for SONiC gNMI"
	@echo ""
	@echo "This makefile supports testing packages without CGO or SONiC dependencies."
	@echo "The goal is to eventually make all packages vanilla-runnable by properly"
	@echo "quarantining CGO/SONiC dependencies."
	@echo ""
	@echo "Available targets:"
	@echo "  ci               - Full CI pipeline (default)"
	@echo "  quick            - Quick validation (fmt, vet, build)"
	@echo "  test             - Run tests with coverage"
	@echo "  test-coverage    - Generate HTML coverage reports"
	@echo "  fmt              - Format code"
	@echo "  fmt-check        - Check code formatting"
	@echo "  vet              - Run static analysis"
	@echo "  lint             - Run linting checks"
	@echo "  build-test       - Test package builds"
	@echo "  bench            - Run benchmarks"
	@echo "  security         - Run security scan (requires gosec)"
	@echo "  mod-verify       - Verify go modules"
	@echo "  list-packages    - List vanilla packages"
	@echo "  clean            - Clean build artifacts"
	@echo "  help             - Show this help"
	@echo ""
	@echo "Examples:"
	@echo "  make -f vanilla.mk ci"
	@echo "  make -f vanilla.mk quick"
	@echo "  make -f vanilla.mk test-coverage"
	@echo "  make -f vanilla.mk PACKAGES=pkg/server/upgrade-handler ci"
	@echo ""
	@echo "Currently vanilla packages:"
	@for pkg in $(VANILLA_PACKAGES); do \
		echo "  - $$pkg"; \
	done