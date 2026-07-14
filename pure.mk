# pure.mk - Simple CI for canonical Go packages without SONiC dependencies
# Usage: make -f pure.mk ci
#
# Every Go package under internal/, pkg/, and cmd/ must remain pure. Packages
# outside those canonical roots may depend on the SONiC build environment.

# Go configuration
GO ?= go
GOROOT ?= $(shell $(GO) env GOROOT)
PURE_CGO_ENABLED ?= 0
RACE_CGO_ENABLED ?= 1
PURE_TAG ?= pure
PURE_GO_FLAGS := -tags=$(PURE_TAG)

# Discover every package under the canonical pure roots. This makes purity a
# path-based invariant instead of an allowlist that can omit new packages.
PURE_ROOTS := internal pkg cmd
PURE_PACKAGES := $(shell \
	for root in $(PURE_ROOTS); do \
		if [ -d "$$root" ]; then \
			find "$$root" -type f -name '*.go' -print; \
		fi; \
	done | sed 's|/[^/]*$$||' | sort -u)

# You can test specific packages by setting PACKAGES=pkg/specific/package
PACKAGES ?= $(PURE_PACKAGES)

# Default target
.DEFAULT_GOAL := ci

# Clean up any build artifacts
.PHONY: clean
clean:
	@echo "Cleaning pure build artifacts..."
	$(GO) clean -cache -testcache
	@set -e; for pkg in $(PACKAGES); do \
		rm -f $$pkg/coverage.out $$pkg/coverage.html; \
	done

# Format check - ensure code is properly formatted
.PHONY: fmt-check
fmt-check:
	@echo "Checking Go code formatting for pure packages..."
	@set -e; for pkg in $(PACKAGES); do \
		echo "Checking $$pkg..."; \
		files=$$($(GOROOT)/bin/gofmt -l $$pkg/*.go); \
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
	@set -e; for pkg in $(PACKAGES); do \
		echo "Formatting $$pkg..."; \
		$(GOROOT)/bin/gofmt -w $$pkg/*.go; \
	done

# Vet - static analysis
.PHONY: vet
vet:
	@echo "Running go vet on pure packages..."
	@set -e; for pkg in $(PACKAGES); do \
		echo "Vetting $$pkg..."; \
		(cd $$pkg && CGO_ENABLED=$(PURE_CGO_ENABLED) $(GO) vet $(PURE_GO_FLAGS) .); \
	done

# Test - run all tests with coverage
.PHONY: test
test:
	@echo "Running tests for pure packages..."
	@set -e; for pkg in $(PACKAGES); do \
		echo ""; \
		echo "=== Testing $$pkg ==="; \
		(cd $$pkg && CGO_ENABLED=$(RACE_CGO_ENABLED) $(GO) test $(PURE_GO_FLAGS) -gcflags="all=-N -l" -v -race -coverprofile=coverage.out -covermode=atomic .); \
		if [ -f $$pkg/coverage.out ]; then \
			echo "Coverage for $$pkg:"; \
			(cd $$pkg && $(GO) tool cover -func=coverage.out); \
		fi; \
	done

# Generate coverage files for Azure pipeline integration
.PHONY: azure-coverage
azure-coverage:
	@echo "Generating coverage files for Azure pipeline..."
	@set -e; for pkg in $(PACKAGES); do \
		echo "Testing $$pkg..."; \
		pkgname=$$(echo $$pkg | tr '/' '-'); \
		CGO_ENABLED=$(RACE_CGO_ENABLED) $(GO) test $(PURE_GO_FLAGS) -gcflags="all=-N -l" -race -coverprofile=coverage-pure-$$pkgname.txt -covermode=atomic -v ./$$pkg; \
	done
	@echo "Coverage files generated for Azure pipeline"

# Test with coverage report
.PHONY: test-coverage
test-coverage: test
	@echo "Generating HTML coverage reports..."
	@set -e; for pkg in $(PACKAGES); do \
		if [ -f $$pkg/coverage.out ]; then \
			echo "Generating coverage report for $$pkg..."; \
			(cd $$pkg && $(GO) tool cover -html=coverage.out -o coverage.html); \
			echo "Coverage report generated: $$pkg/coverage.html"; \
		fi; \
	done

# Generate XML coverage report for Azure pipelines
.PHONY: coverage-xml
coverage-xml: test
	@echo "Generating XML coverage report for Azure..."
	@set -e; if command -v gocov >/dev/null 2>&1 && command -v gocov-xml >/dev/null 2>&1; then \
		echo "Converting coverage to XML format..."; \
		rm -f coverage-*.out; \
		for pkg in $(PACKAGES); do \
			if [ -f $$pkg/coverage.out ]; then \
				pkgname=$$(echo $$pkg | tr '/' '-'); \
				cp $$pkg/coverage.out coverage-$$pkgname.out; \
			fi; \
		done; \
		if ls coverage-*.out >/dev/null 2>&1; then \
			trap 'rm -f coverage-*.out coverage-pure.json' EXIT; \
			gocov convert coverage-*.out > coverage-pure.json; \
			gocov-xml -source $(shell pwd) < coverage-pure.json > coverage.xml; \
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
	@set -e; for pkg in $(PACKAGES); do \
		echo "Building $$pkg..."; \
		(cd $$pkg && CGO_ENABLED=$(PURE_CGO_ENABLED) $(GO) build $(PURE_GO_FLAGS) -v .); \
	done

# Lint check using basic go tools
.PHONY: lint
lint: fmt-check
	@echo "Basic linting complete for pure packages"

# Benchmark tests
.PHONY: bench
bench:
	@echo "Running benchmarks for pure packages..."
	@set -e; for pkg in $(PACKAGES); do \
		echo "Benchmarking $$pkg..."; \
		(cd $$pkg && CGO_ENABLED=$(PURE_CGO_ENABLED) $(GO) test $(PURE_GO_FLAGS) -bench=. -benchmem .); \
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
	@set -e; if command -v gosec >/dev/null 2>&1; then \
		for pkg in $(PACKAGES); do \
			echo "Scanning $$pkg..."; \
			(cd $$pkg && gosec -tags=$(PURE_TAG) .); \
		done; \
	else \
		echo "gosec not available, skipping security scan"; \
		echo "Install with: go install github.com/securecodewarrior/gosec/v2/cmd/gosec@latest"; \
	fi

# Verify that canonical packages do not depend on SONiC-only modules.
.PHONY: check-deps
check-deps:
	@echo "Checking pure dependency graph..."
	@set -e; \
	deps=$$(CGO_ENABLED=$(PURE_CGO_ENABLED) $(GO) list $(PURE_GO_FLAGS) -test -deps $(addprefix ./,$(PACKAGES))); \
	for dep in $$deps; do \
	for forbidden in \
		github.com/sonic-net/sonic-gnmi/swsscommon \
		github.com/Azure/sonic-mgmt-common; do \
			case "$$dep" in \
				"$$forbidden"|"$$forbidden"/*) \
					echo "Pure package graph includes forbidden dependency: $$dep"; \
					exit 1; \
					;; \
			esac; \
		done; \
	done
	@echo "Pure dependency graph is clean."

# Keep pure build constraints on provider adapters rather than business logic.
.PHONY: check-tags
check-tags:
	@echo "Checking pure build-tag placement..."
	@set -e; \
	files=$$(grep -R -l '^//go:build .*pure' $(PURE_ROOTS) --include='*.go' 2>/dev/null || true); \
	for file in $$files; do \
		case "$$file" in \
			internal/*/provider_*.go) ;; \
			*) \
				echo "Pure build tag is outside an internal provider adapter: $$file"; \
				exit 1; \
				;; \
		esac; \
	done
	@echo "Pure build-tag placement is clean."

# List pure packages
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
ci: clean check-deps check-tags lint build-test test
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
junit-xml: clean check-deps check-tags build-test
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
	CGO_ENABLED=$(RACE_CGO_ENABLED) gotestsum --junitfile test-results/junit-pure.xml \
		--format testname \
		-- $(PURE_GO_FLAGS) -gcflags="all=-N -l" -v -race \
		-coverprofile=test-results/coverage-pure.txt \
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
quick: check-deps check-tags fmt-check vet build-test
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
	@echo "  check-deps       - Reject SONiC-only dependencies from pure packages"
	@echo "  check-tags       - Restrict pure tags to provider adapters"
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
