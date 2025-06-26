.PHONY: proto build run clean tools test test-e2e fmt vet tidy verify ci mocks validate-mocks docker-build docker-push \
	install-protoc-gen-go install-protoc-gen-go-grpc install-mockgen install-protoc install-golangci-lint help-tools \
	validate-protoc validate-protoc-gen-go validate-protoc-gen-go-grpc validate-mockgen validate-golangci-lint validate-protobuf-tools validate-tools \
	lint

# Default go build tags
BUILD_TAGS ?=
PROTOC_VERSION ?= 31.1
PROTOC_GEN_GO_VERSION ?= v1.36.6
PROTOC_GEN_GO_GRPC_VERSION ?= v1.5.1
MOCKGEN_VERSION ?= v0.5.2
MOCKGEN_PACKAGE ?= go.uber.org/mock/mockgen
GOLANGCI_LINT_VERSION ?= v2.1.6
DOCKER_TAG ?= latest
DOCKER_IMAGE ?= docker-mopd

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

# Check formatting with gofmt
fmt:
	@echo "Checking formatting (gofmt)..."
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "ERROR: gofmt issues found in the following files:"; \
		echo "$$UNFORMATTED"; \
		gofmt -d .; \
		exit 1; \
	fi

# Install protoc compiler
install-protoc:
	@echo "Installing protoc v$(PROTOC_VERSION)..."
	@PROTOC_ZIP="protoc-$(PROTOC_VERSION)-linux-x86_64.zip"; \
	curl -OL "https://github.com/protocolbuffers/protobuf/releases/download/v$(PROTOC_VERSION)/$$PROTOC_ZIP"; \
	sudo unzip -o $$PROTOC_ZIP -d /usr/local bin/protoc; \
	sudo unzip -o $$PROTOC_ZIP -d /usr/local 'include/*'; \
	rm -f $$PROTOC_ZIP

# Generate protobuf files
proto: validate-protobuf-tools
	PATH="$(shell go env GOPATH)/bin:$$PATH" protoc --go_out=. --go_opt=paths=source_relative \
        --go-grpc_out=. --go-grpc_opt=paths=source_relative \
        proto/*.proto

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

# Validate proto-generated files are up to date
validate-proto: validate-protobuf-tools
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

# Tidy modules - cleans up unused dependencies in go.mod and go.sum
tidy:
	go mod tidy

# Run static analysis
vet:
	go vet ./...

# Run golangci-lint
lint: validate-golangci-lint
	golangci-lint run

# Run all tests
test:
	go test ./... -timeout 2m

# Run end-to-end tests
test-e2e:
	go test ./tests/e2e/... -timeout 2m

# Verify modules
verify:
	go mod verify

# Generate mocks for interfaces
# Define mock configurations as:
# packagePath:interfaceName:outputDir
MOCK_CONFIGS := \
	internal/hostinfo/platform.go:PlatformInfoProvider:internal/hostinfo/mocks

mocks: validate-mockgen
	@echo "Generating mocks..."
	@for config in $(MOCK_CONFIGS); do \
		source=$$(echo $$config | cut -d: -f1); \
		interfaces=$$(echo $$config | cut -d: -f2); \
		output_dir=$$(echo $$config | cut -d: -f3); \
		output_file="mock_$$(basename $$source .go).go"; \
		echo "  Generating mock for $$interfaces from $$source"; \
		mkdir -p $$output_dir; \
		GO111MODULE=on mockgen \
			-source=$$source \
			-destination=$$output_dir/$$output_file \
			-package=$$(basename $$output_dir) \
			$$interfaces; \
	done
	@echo "Mock generation complete!"

# Validate mock-generated files are up to date
validate-mocks: validate-mockgen
	@echo "Validating mock-generated files..."
	@TEMP_DIR=$$(mktemp -d); \
	DIFF_FILES=false; \
	for config in $(MOCK_CONFIGS); do \
		source=$$(echo $$config | cut -d: -f1); \
		interfaces=$$(echo $$config | cut -d: -f2); \
		output_dir=$$(echo $$config | cut -d: -f3); \
		output_file="mock_$$(basename $$source .go).go"; \
		temp_output_dir="$$TEMP_DIR/$$output_dir"; \
		mkdir -p "$$temp_output_dir"; \
		GO111MODULE=on mockgen \
			-source=$$source \
			-destination="$$temp_output_dir/$$output_file" \
			-package=$$(basename $$output_dir) \
			$$interfaces; \
		if [ ! -f "$$output_dir/$$output_file" ]; then \
			echo "Generated mock file $$output_dir/$$output_file does not exist"; \
			DIFF_FILES=true; \
		else \
			TEMP_CURRENT=$$(mktemp); \
			TEMP_GENERATED=$$(mktemp); \
			sed 's|-destination=[^[:space:]]*|-destination=NORMALIZED_PATH|g' "$$output_dir/$$output_file" > "$$TEMP_CURRENT"; \
			sed 's|-destination=[^[:space:]]*|-destination=NORMALIZED_PATH|g' "$$temp_output_dir/$$output_file" > "$$TEMP_GENERATED"; \
			if ! diff -u "$$TEMP_CURRENT" "$$TEMP_GENERATED" > /dev/null; then \
				echo "Differences found in $$output_dir/$$output_file:"; \
				diff -u "$$output_dir/$$output_file" "$$temp_output_dir/$$output_file"; \
				DIFF_FILES=true; \
			fi; \
			rm -f "$$TEMP_CURRENT" "$$TEMP_GENERATED"; \
		fi; \
	done; \
	if [ "$$DIFF_FILES" = true ]; then \
		echo "ERROR: Mock-generated files are not up to date. Please regenerate them locally and commit the changes."; \
		echo "Run 'make mocks' and commit the changes."; \
		rm -rf $$TEMP_DIR; \
		exit 1; \
	else \
		echo "Mock-generated files are up to date."; \
	fi; \
	rm -rf $$TEMP_DIR

# Build the server
build: validate-proto
	go build -tags "$(BUILD_TAGS)" -o bin/server cmd/server/main.go

# Build all packages
build-all:
	go build ./...

# Run the server
run: build
	./bin/server

# Clean build artifacts
clean:
	rm -f bin/server
	rm -f proto/*.pb.go

# CI pipeline target - runs all verification steps (validation only, no tool installation)
ci: fmt validate-proto validate-mocks tidy build-all vet lint test test-e2e verify

# Build Docker image
docker-build: build
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -f docker/Dockerfile .

# Push Docker image to registry
docker-push: docker-build
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
