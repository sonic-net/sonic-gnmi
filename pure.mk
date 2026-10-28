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
HOST_GOOS = $(shell $(GO) env GOHOSTOS)
HOST_GOARCH = $(shell $(GO) env GOHOSTARCH)
HOST_GO_ENV = GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH)
TOOLS_BIN_DIR := $(abspath build/tools/bin)
GOTESTSUM := $(TOOLS_BIN_DIR)/gotestsum-v1.12.3
GOCOV := $(TOOLS_BIN_DIR)/gocov-v1.1.0
GOCOV_XML := $(TOOLS_BIN_DIR)/gocov-xml-v1.1.0
GOSEC := $(TOOLS_BIN_DIR)/gosec-v2.28.0

$(TOOLS_BIN_DIR):
	@mkdir -p $@

$(GOTESTSUM): | $(TOOLS_BIN_DIR)
	@$(HOST_GO_ENV) GOBIN=$(TOOLS_BIN_DIR) $(GO) install gotest.tools/gotestsum@v1.12.3
	@mv $(TOOLS_BIN_DIR)/gotestsum $@

$(GOCOV): | $(TOOLS_BIN_DIR)
	@$(HOST_GO_ENV) GOBIN=$(TOOLS_BIN_DIR) $(GO) install github.com/axw/gocov/gocov@v1.1.0
	@mv $(TOOLS_BIN_DIR)/gocov $@

$(GOCOV_XML): | $(TOOLS_BIN_DIR)
	@$(HOST_GO_ENV) GOBIN=$(TOOLS_BIN_DIR) $(GO) install github.com/AlekSi/gocov-xml@v1.1.0
	@mv $(TOOLS_BIN_DIR)/gocov-xml $@

$(GOSEC): | $(TOOLS_BIN_DIR)
	@$(HOST_GO_ENV) GOBIN=$(TOOLS_BIN_DIR) $(GO) install github.com/securego/gosec/v2/cmd/gosec@v2.28.0
	@mv $(TOOLS_BIN_DIR)/gosec $@

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

# Explicitly clear Go's shared build/test caches and package coverage artifacts.
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
test: build-test
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
azure-coverage: build-test
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
coverage-xml: test $(GOCOV) $(GOCOV_XML)
	@echo "Generating XML coverage report for Azure..."
	@set -e; echo "Converting coverage to XML format..."; \
	 rm -f coverage-*.out; \
	 for pkg in $(PACKAGES); do \
		if [ -f $$pkg/coverage.out ]; then \
			pkgname=$$(echo $$pkg | tr '/' '-'); \
			cp $$pkg/coverage.out coverage-$$pkgname.out; \
		fi; \
	 done; \
	 if ls coverage-*.out >/dev/null 2>&1; then \
		trap 'rm -f coverage-*.out coverage-pure.json' EXIT; \
		$(GOCOV) convert coverage-*.out > coverage-pure.json; \
		$(GOCOV_XML) -source $(shell pwd) < coverage-pure.json > coverage.xml; \
		echo "XML coverage report generated: coverage.xml"; \
	 else \
		echo "No coverage files found"; \
	fi

# Build test - ensure the package builds
.PHONY: build-test
build-test:
	@echo "Testing build of pure packages and tests..."
	@set -e; for pkg in $(PACKAGES); do \
		echo "Building $$pkg..."; \
		(cd $$pkg && CGO_ENABLED=$(PURE_CGO_ENABLED) $(GO) build $(PURE_GO_FLAGS) -v .); \
		(cd $$pkg && CGO_ENABLED=$(PURE_CGO_ENABLED) $(GO) test $(PURE_GO_FLAGS) -c -o /dev/null .); \
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

# Security scan using the pinned gosec tool
.PHONY: security
security: $(GOSEC)
	@echo "Running security scan on pure packages..."
	@set -e; for pkg in $(PACKAGES); do \
		echo "Scanning $$pkg..."; \
		(cd $$pkg && $(GOSEC) -tags=$(PURE_TAG) .); \
	done

# Keep pure build constraints under internal/adaptors rather than business logic.
.PHONY: check-tags
check-tags:
	@echo "Checking pure build-tag placement..."
	@set -e; \
	files=$$(find $(PURE_ROOTS) -type f -name '*.go' -exec grep -El '^[[:blank:]]*//(go:build[[:blank:]]+|[[:blank:]]*\+build[[:blank:]]+).*pure' {} + 2>/dev/null || true); \
	for file in $$files; do \
		case "$$file" in \
			internal/adaptors/*/provider_*.go) \
				relative=$${file#internal/adaptors/}; \
				case "$$relative" in */*/*) ;; *) continue ;; esac; \
				;; \
		esac; \
		echo "Pure build tag is outside a direct internal adaptor provider: $$file"; \
		exit 1; \
	done
	@echo "Pure build-tag placement is clean."

.PHONY: check-tags-test
check-tags-test:
	python3 tools/test/test_pure_tags.py

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
ci: check-tags check-tags-test lint build-test test
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

# Complete pure CI checks, test execution and report generation.
.PHONY: ci-report
ci-report:
	@rm -f test-results/junit-pure.xml test-results/coverage-pure.txt test-results/coverage-pure.xml
	@$(MAKE) -f pure.mk check-tags check-tags-test
	@$(MAKE) -f pure.mk build-test
	@$(MAKE) -f pure.mk test-pure
	@$(MAKE) -f pure.mk report-coverage
	@echo "Pure CI checks, tests and reports completed."

.PHONY: test-pure
test-pure:
	@rm -f test-results/junit-pure.xml test-results/coverage-pure.txt test-results/coverage-pure.xml
	@$(MAKE) -f pure.mk $(GOTESTSUM)
	@echo "Running pure package tests with JUnit XML output..."
	@mkdir -p test-results
	@CGO_ENABLED=$(RACE_CGO_ENABLED) $(GOTESTSUM) --junitfile test-results/junit-pure.xml \
		--format testname \
		-- $(PURE_GO_FLAGS) -gcflags="all=-N -l" -v -race \
		-coverprofile=test-results/coverage-pure.txt \
		-covermode=atomic \
		$(addprefix ./,$(PACKAGES)) || { \
			status=$$?; rm -f test-results/coverage-pure.txt; exit $$status; \
		}

.PHONY: report-coverage
report-coverage:
	@set -e; \
	rm -f test-results/coverage-pure.xml; \
	test -s test-results/coverage-pure.txt; \
	$(MAKE) -f pure.mk $(GOCOV) $(GOCOV_XML); \
	json=$$(mktemp test-results/coverage-pure-json.XXXXXX); \
	xml=; \
	trap 'rm -f "$$json" "$$xml"' EXIT; \
	xml=$$(mktemp test-results/coverage-pure-xml.XXXXXX); \
	$(GOCOV) convert test-results/coverage-pure.txt > "$$json"; \
	$(GOCOV_XML) -source "$(CURDIR)" < "$$json" > "$$xml"; \
	mv "$$xml" test-results/coverage-pure.xml; \
	echo "Coverage report generated: test-results/coverage-pure.xml"

# Quick check for development
.PHONY: quick
quick: check-tags check-tags-test fmt-check vet build-test
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
	@echo "  ci-report        - Check, build and test pure packages; generate CI reports"
	@echo "  test-pure        - Run race-enabled pure tests; write JUnit and raw coverage"
	@echo "  report-coverage  - Convert existing pure coverage without running tests"
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
	@echo "  security         - Run security scan"
	@echo "  check-tags       - Restrict pure tags to internal adaptors"
	@echo "  check-tags-test  - Test pure build-tag placement checks"
	@echo "  mod-verify       - Verify go modules"
	@echo "  list-packages    - List pure packages"
	@echo "  clean            - Clear shared Go build/test caches and package coverage"
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
