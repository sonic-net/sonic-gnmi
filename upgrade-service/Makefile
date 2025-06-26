.PHONY: proto build run clean test test-coverage coverage-html coverage-summary validate-coverage test-e2e format validate-format vet tidy verify ci mocks validate-mocks docker-build docker-push lint deb

# Include tool management makefile
include tools.mk

# Default go build tags
BUILD_TAGS ?=
COVERAGE_THRESHOLD ?= 25
DOCKER_TAG ?= latest
DOCKER_IMAGE ?= opsd


# Fix formatting with gofmt
format:
	@echo "Fixing formatting (gofmt)..."
	@gofmt -w .
	@echo "Formatting fixed."

# Validate formatting with gofmt (for CI)
validate-format:
	@echo "Validating formatting (gofmt)..."
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "ERROR: gofmt issues found in the following files:"; \
		echo "$$UNFORMATTED"; \
		gofmt -d .; \
		exit 1; \
	fi


# Generate protobuf files
proto: validate-protobuf-tools
	PATH="$(shell go env GOPATH)/bin:$$PATH" protoc --go_out=. --go_opt=paths=source_relative \
        --go-grpc_out=. --go-grpc_opt=paths=source_relative \
        proto/*.proto


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

# Run all tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	@go test ./... -timeout 2m -coverprofile=coverage.out -covermode=atomic
	@echo "Coverage report generated: coverage.out"

# Generate HTML coverage report
coverage-html: test-coverage
	@echo "Generating HTML coverage report..."
	@go tool cover -html=coverage.out -o coverage.html
	@echo "HTML coverage report generated: coverage.html"

# Show coverage summary
coverage-summary: test-coverage
	@echo "Coverage summary:"
	@go tool cover -func=coverage.out

# Validate coverage meets minimum threshold
validate-coverage: test-coverage
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
	go build -tags "$(BUILD_TAGS)" -o bin/sonic-ops-server cmd/server/main.go

# Build all packages
build-all:
	go build ./...

# Run the server
run: build
	./bin/sonic-ops-server

# Clean build artifacts
clean:
	rm -rf bin/

# CI pipeline target - runs all verification steps (validation only, no tool installation)
ci: validate-format validate-proto validate-mocks tidy build-all vet lint test validate-coverage test-e2e verify

# Build Docker image
docker-build: build
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -f docker/Dockerfile .

# Push Docker image to registry
docker-push: docker-build
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)

# Build debian package
deb: build
	@echo "Building debian package..."
	@mkdir -p build
	@rm -rf build/*
	@dpkg-buildpackage -us -uc -d --build-dir=build 2>/dev/null || dpkg-buildpackage -us -uc -d
	@if [ -f ../sonic-ops-server_*.deb ]; then \
		mv ../sonic-ops-server_*.* build/ 2>/dev/null || true; \
	fi
	@echo "Debian package built in build/ directory"
