# Tool management makefile
# This file contains all tool installation and validation targets

# Tool versions
PROTOC_VERSION ?= 31.1
PROTOC_GEN_GO_VERSION ?= v1.36.6
PROTOC_GEN_GO_GRPC_VERSION ?= v1.5.1
MOCKGEN_VERSION ?= v0.5.2
MOCKGEN_PACKAGE ?= go.uber.org/mock/mockgen
GOLANGCI_LINT_VERSION ?= v2.1.6

# Version check macro
# Usage: $(call check-tool-version,tool-name,expected-version,install-target)
define check-tool-version
	@echo "Validating $(1) $(2)..."
	@which $(1) > /dev/null || (echo "ERROR: $(1) not found. Run 'make $(3)' to install $(2)."; exit 1)
	@INSTALLED_VERSION=$$($(1) --version 2>&1 | grep -o -E '(v?[0-9]+\.[0-9]+\.[0-9]+)' | head -1); \
	EXPECTED_VERSION=$(2); \
	if [ "$$INSTALLED_VERSION" = "$${EXPECTED_VERSION#v}" ] || [ "v$$INSTALLED_VERSION" = "$$EXPECTED_VERSION" ] || [ "$$INSTALLED_VERSION" = "$$EXPECTED_VERSION" ]; then \
		echo "✓ $(1) version $$INSTALLED_VERSION matches expected $(2)"; \
	else \
		echo "WARNING: $(1) version mismatch. Expected $(2), found $$INSTALLED_VERSION"; \
		echo "Run 'make $(3)' to install the correct version."; \
	fi
endef

# Install protoc compiler
install-protoc:
	@echo "Installing protoc v$(PROTOC_VERSION)..."
	@PROTOC_ZIP="protoc-$(PROTOC_VERSION)-linux-x86_64.zip"; \
	curl -OL "https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/$$PROTOC_ZIP"; \
	sudo unzip -o $$PROTOC_ZIP -d /usr/local bin/protoc; \
	sudo unzip -o $$PROTOC_ZIP -d /usr/local 'include/*'; \
	rm -f $$PROTOC_ZIP

# Install protoc-gen-go
install-protoc-gen-go:
	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	@echo "protoc-gen-go $(PROTOC_GEN_GO_VERSION) installed to $(shell go env GOPATH)/bin"

# Install protoc-gen-go-grpc
install-protoc-gen-go-grpc:
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)
	@echo "protoc-gen-go-grpc $(PROTOC_GEN_GO_GRPC_VERSION) installed to $(shell go env GOPATH)/bin"

# Install mockgen
install-mockgen:
	go install $(MOCKGEN_PACKAGE)@$(MOCKGEN_VERSION)
	@echo "mockgen $(MOCKGEN_VERSION) from $(MOCKGEN_PACKAGE) installed to $(shell go env GOPATH)/bin"

# Install golangci-lint
install-golangci-lint:
	@echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION)..."
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(shell go env GOPATH)/bin $(GOLANGCI_LINT_VERSION)
	@echo "golangci-lint $(GOLANGCI_LINT_VERSION) installed to $(shell go env GOPATH)/bin"

# Install all tools
tools: install-protoc-gen-go install-protoc-gen-go-grpc install-mockgen install-golangci-lint
	@echo "All tools installed to $(shell go env GOPATH)/bin"

# Validate protoc is available
validate-protoc:
	@echo "Validating protoc..."
	@which protoc > /dev/null || (echo "ERROR: protoc not found. Run 'make install-protoc' to install."; exit 1)

# Validate protoc-gen-go is available
validate-protoc-gen-go:
	$(call check-tool-version,protoc-gen-go,$(PROTOC_GEN_GO_VERSION),install-protoc-gen-go)

# Validate protoc-gen-go-grpc is available
validate-protoc-gen-go-grpc:
	$(call check-tool-version,protoc-gen-go-grpc,$(PROTOC_GEN_GO_GRPC_VERSION),install-protoc-gen-go-grpc)

# Validate mockgen is available
validate-mockgen:
	$(call check-tool-version,mockgen,$(MOCKGEN_VERSION),install-mockgen)

# Validate golangci-lint is available
validate-golangci-lint:
	$(call check-tool-version,golangci-lint,$(GOLANGCI_LINT_VERSION),install-golangci-lint)

# Validate all protobuf tools are available
validate-protobuf-tools: validate-protoc validate-protoc-gen-go validate-protoc-gen-go-grpc

# Validate all tools are available
validate-tools: validate-protobuf-tools validate-mockgen validate-golangci-lint

# Show help for tool management
help-tools:
	@echo "Available tool management targets:"
	@echo "  install-protoc              - Install protoc compiler"
	@echo "  install-protoc-gen-go       - Install protoc-gen-go plugin"
	@echo "  install-protoc-gen-go-grpc  - Install protoc-gen-go-grpc plugin"
	@echo "  install-mockgen             - Install mockgen tool"
	@echo "  install-golangci-lint       - Install golangci-lint linter"
	@echo "  tools                       - Install all Go tools (excludes protoc)"
	@echo ""
	@echo "  validate-protoc             - Check if protoc is available"
	@echo "  validate-protoc-gen-go      - Check if protoc-gen-go is available"
	@echo "  validate-protoc-gen-go-grpc - Check if protoc-gen-go-grpc is available"
	@echo "  validate-mockgen            - Check if mockgen is available"
	@echo "  validate-golangci-lint      - Check if golangci-lint is available"
	@echo "  validate-protobuf-tools     - Check all protobuf tools"
	@echo "  validate-tools              - Check all tools"