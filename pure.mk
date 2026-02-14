# pure.mk - Simple CI for pure packages without SONiC dependencies
# Usage: make -f pure.mk ci
#
# This makefile supports testing packages that don't require CGO or SONiC dependencies.
# Add new pure packages to PURE_PACKAGES below.
#
# Goal: Eventually all packages should be pure unless they absolutely
# require CGO dependencies. All CGO/SONiC dependencies should be properly quarantined.

# Go configuration
GO ?= go
GOROOT ?= $(shell $(GO) env GOROOT)

# Pure packages (no CGO/SONiC dependencies)
# Add new packages here as they become pure-compatible.
PURE_PACKAGES := \
	internal/exec \
	pkg/gnoi/debug \
	pkg/bypass \
	internal/diskspace \
	internal/hash \
	internal/download \
	internal/firmware \
	pkg/interceptors \
	pkg/server/operational-handler \
	pkg/gnoi/file \
	pkg/exec \
	pkg/gnoi/os \
	pkg/gnoi/system

# Future packages to make pure:
# TODO: sonic-gnmi-standalone/pkg/workflow
# TODO: sonic-gnmi-standalone/pkg/client/config
# TODO: sonic-gnmi-standalone/internal/checksum
# TODO: sonic-gnmi-standalone/internal/download
# TODO: common_utils (parts that don't need CGO)
# TODO: gnoi_client/config
# TODO: transl_utils (isolate from translib dependencies)
# TODO: pkg/interceptors/dpuproxy (needs gRPC infrastructure mocking)

# You can test specific packages by setting PACKAGES=pkg/specific/package
PACKAGES ?= $(PURE_PACKAGES)

# Default target
.DEFAULT_GOAL := ci

# Clean up any build artifacts
.PHONY: clean
clean:
	@echo "Cleaning pure build artifacts..."
	$(GO) clean -cache -testcache
	@for pkg in $(PACKAGES); do \
		rm -f $$pkg/coverage.out $$pkg/coverage.html; \
	done

# Format check - ensure code is properly formatted
.PHONY: fmt-check
fmt-check:
	@echo "Checking Go code formatting for pure packages..."
	@for pkg in $(PACKAGES); do \
		echo "Checking $$pkg..."; \
		files=$$($(GOROOT)/bin/gofmt -l $$pkg/*.go 2>/dev/null || true); \
		if [ -n "$$files" ]; then \
			echo "The following files need formatting in $$pkg:"; \
			echo "$$files"; \
			echo "Please run 'make -f pure.mk fmt' or 'gofmt -w $$pkg/*.go'"; \
			exit 1; \
		fi; \
	done
	@echo "All files are properly formatted."

# Format code
.PHONY: fmt
fmt:
	@echo "Formatting Go code for pure packages..."
	@for pkg in $(PACKAGES); do \
		echo "Formatting $$pkg..."; \
		$(GOROOT)/bin/gofmt -w $$pkg/*.go 2>/dev/null || true; \
	done

# Vet - static analysis
.PHONY: vet
vet:
	@echo "Running go vet on pure packages..."
	@for pkg in $(PACKAGES); do \
		echo "Vetting $$pkg..."; \
		cd $$pkg && $(GO) vet ./...; \
		cd - >/dev/null; \
	done

# Test - run all tests with coverage
.PHONY: test
test:
	@echo "Running tests for pure packages..."
	@for pkg in $(PACKAGES); do \
		echo ""; \
		echo "=== Testing $$pkg ==="; \
		cd $$pkg && $(GO) test -gcflags="all=-N -l" -v -race -coverprofile=coverage.out -covermode=atomic ./...; \
		if [ -f coverage.out ]; then \
			echo "Coverage for $$pkg:"; \
			$(GO) tool cover -func=coverage.out; \
		fi; \
		cd - >/dev/null; \
	done

# Generate coverage files for Azure pipeline integration
.PHONY: azure-coverage
azure-coverage:
	@echo "Generating coverage files for Azure pipeline..."
	@for pkg in $(PACKAGES); do \
		echo "Testing $$pkg..."; \
		pkgname=$$(echo $$pkg | tr '/' '-'); \
		$(GO) test -gcflags="all=-N -l" -race -coverprofile=coverage-pure-$$pkgname.txt -covermode=atomic -v ./$$pkg; \
	done
	@echo "Coverage files generated for Azure pipeline"

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

# Generate XML coverage report for Azure pipelines
.PHONY: coverage-xml
coverage-xml: test
	@echo "Generating XML coverage report for Azure..."
	@if command -v gocov >/dev/null 2>&1 && command -v gocov-xml >/dev/null 2>&1; then \
		echo "Converting coverage to XML format..."; \
		rm -f coverage-*.out; \
		for pkg in $(PACKAGES); do \
			if [ -f $$pkg/coverage.out ]; then \
				pkgname=$$(echo $$pkg | tr '/' '-'); \
				cp $$pkg/coverage.out coverage-$$pkgname.out; \
			fi; \
		done; \
		if ls coverage-*.out >/dev/null 2>&1; then \
			gocov convert coverage-*.out | gocov-xml -source $(shell pwd) > coverage.xml; \
			rm -f coverage-*.out; \
			echo "XML coverage report generated: coverage.xml"; \
		else \
			echo "No coverage files found"; \
		fi; \
	else \
		echo "Warning: gocov and gocov-xml not available"; \
		echo "Install with: go install github.com/axw/gocov/gocov@latest"; \
		echo "              go install github.com/AlekSi/gocov-xml@latest"; \
	fi

# Build test - ensure the package builds
.PHONY: build-test
build-test:
	@echo "Testing build of pure packages..."
	@for pkg in $(PACKAGES); do \
		echo "Building $$pkg..."; \
		cd $$pkg && $(GO) build -v ./...; \
		cd - >/dev/null; \
	done

# Lint check using basic go tools
.PHONY: lint
lint: fmt-check
	@echo "Basic linting complete for pure packages"

# Benchmark tests
.PHONY: bench
bench:
	@echo "Running benchmarks for pure packages..."
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
	@echo "Running security scan on pure packages..."
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
	@echo "Pure packages:"
	@for pkg in $(PURE_PACKAGES); do \
		echo "  - $$pkg"; \
	done
	@echo ""
	@echo "Currently testing packages:"
	@for pkg in $(PACKAGES); do \
		echo "  - $$pkg"; \
	done


# Full CI pipeline
.PHONY: ci
ci: clean lint build-test test
	@echo ""
	@echo "============================================="
	@echo "✅ Pure CI completed successfully!"
	@echo "============================================="
	@echo "Tested packages:"
	@for pkg in $(PACKAGES); do \
		echo "  - $$pkg"; \
	done
	@echo ""
	@echo "Components validated:"
	@echo "  - Code formatting"
	@echo "  - Build verification"
	@echo "  - Unit tests with race detection"
	@echo "  - Test coverage analysis"
	@echo ""
	@echo "This validates that these packages can be developed"
	@echo "and tested without SONiC/CGO dependencies."

# JUnit XML output for Azure Pipelines test reporting
# Note: The Azure pipeline now calls gotestsum directly with set -euo pipefail
# This target is kept for local testing convenience
.PHONY: junit-xml
junit-xml: clean
	@echo "Installing gotestsum for JUnit XML generation..."
	@if ! command -v gotestsum >/dev/null 2>&1; then \
		$(GO) install gotest.tools/gotestsum@v1.11.0; \
	fi
	@echo "Installing gocov tools for coverage conversion..."
	@if ! command -v gocov >/dev/null 2>&1; then \
		$(GO) install github.com/axw/gocov/gocov@v1.1.0; \
	fi
	@if ! command -v gocov-xml >/dev/null 2>&1; then \
		$(GO) install github.com/AlekSi/gocov-xml@v1.1.0; \
	fi
	@echo "Running pure package tests with JUnit XML output..."
	@mkdir -p test-results
	@export PATH=$(PATH):$(shell $(GO) env GOPATH)/bin && \
	gotestsum --junitfile test-results/junit-pure.xml \
		--format testname \
		-- -v -race -coverprofile=test-results/coverage-pure.txt \
		-covermode=atomic \
		$(addprefix ./,$(PACKAGES))
	@echo "Converting coverage to Cobertura XML format..."
	@export PATH=$(PATH):$(shell $(GO) env GOPATH)/bin && \
	if [ -f test-results/coverage-pure.txt ]; then \
		gocov convert test-results/coverage-pure.txt | gocov-xml -source $(shell pwd) > test-results/coverage-pure.xml; \
		echo "Coverage XML generated: test-results/coverage-pure.xml"; \
	fi
	@echo ""
	@echo "============================================="
	@echo "✅ JUnit XML generation completed!"
	@echo "============================================="
	@echo "Files generated:"
	@echo "  - test-results/junit-pure.xml (JUnit test results)"
	@echo "  - test-results/coverage-pure.txt (Coverage data)"
	@echo "  - test-results/coverage-pure.xml (Cobertura coverage for Azure)"
	@echo ""
	@echo "Tested packages:"
	@for pkg in $(PACKAGES); do \
		echo "  - $$pkg"; \
	done

# Quick check for development
.PHONY: quick
quick: fmt-check vet build-test
	@echo "Quick validation complete for pure packages"

# Help target
.PHONY: help
help:
	@echo "Pure CI Makefile for SONiC gNMI"
	@echo ""
	@echo "This makefile supports testing packages without CGO or SONiC dependencies."
	@echo "The goal is to eventually make all packages pure by properly"
	@echo "quarantining CGO/SONiC dependencies."
	@echo ""
	@echo "Available targets:"
	@echo "  ci               - Full CI pipeline (default)"
	@echo "  junit-xml        - Generate JUnit XML for Azure Pipelines"
	@echo "  quick            - Quick validation (fmt, vet, build)"
	@echo "  test             - Run tests with coverage"
	@echo "  test-coverage    - Generate HTML coverage reports"
	@echo "  coverage-xml     - Generate XML coverage for Azure"
	@echo "  fmt              - Format code"
	@echo "  fmt-check        - Check code formatting"
	@echo "  vet              - Run static analysis"
	@echo "  lint             - Run linting checks"
	@echo "  build-test       - Test package builds"
	@echo "  bench            - Run benchmarks"
	@echo "  security         - Run security scan (requires gosec)"
	@echo "  mod-verify       - Verify go modules"
	@echo "  list-packages    - List pure packages"
	@echo "  clean            - Clean build artifacts"
	@echo "  help             - Show this help"
	@echo ""
	@echo "Examples:"
	@echo "  make -f pure.mk ci"
	@echo "  make -f pure.mk quick"
	@echo "  make -f pure.mk test-coverage"
	@echo "  make -f pure.mk PACKAGES=pkg/server/operational-handler ci"
	@echo ""
	@echo "Currently pure packages:"
	@for pkg in $(PURE_PACKAGES); do \
		echo "  - $$pkg"; \
	done
