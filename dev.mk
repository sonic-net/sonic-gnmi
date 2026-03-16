# dev.mk — sonic-gnmi developer workflow
#
# Usage:
#   make -f dev.mk dev-up          # start the dev container (pull image if needed)
#   make -f dev.mk build           # build the .deb inside the container
#   make -f dev.mk test PKG=gnmi_server TEST=TestServerUnixSocket
#   make -f dev.mk test PKG=gnmi_server   # run all tests in a package
#   make -f dev.mk test-all        # run the full integration test suite (~30 min)
#   make -f dev.mk shell           # open an interactive shell in the container
#   make -f dev.mk dev-down        # stop + remove the container
#   make -f dev.mk dev-image       # build the dev image locally (no CI needed)

# ── Configuration ──────────────────────────────────────────────────────────────
# Pre-built image hosted on sonicdev-microsoft ACR (publicly pullable, no auth):
#   sonicdev-microsoft.azurecr.io:443/sonic-gnmi-dev:latest
# Rebuilt by CI on every push to master/release branches.
DEV_IMAGE      ?= sonicdev-microsoft.azurecr.io:443/sonic-gnmi-dev:latest
CONTAINER_NAME ?= sonic-gnmi-dev
WORKSPACE      := $(abspath .)
BUILD_BRANCH   ?= master

# CGO flags required for all go test invocations
CGO_ENV := CGO_LDFLAGS='-lswsscommon -lhiredis' \
           CGO_CXXFLAGS='-I/usr/include/swss -w -Wall -fpermissive' \
           CGO_CFLAGS='-I/usr/include/swss'
# -gcflags=all=-l disables inlining, required for gomonkey-based mocks in gnmi_server tests
GOMONKEY_FLAGS := -gcflags=all=-l

# ── Helpers ────────────────────────────────────────────────────────────────────
_container_running = $(shell docker ps --filter "name=^$(CONTAINER_NAME)$$" --format '{{.Names}}' 2>/dev/null)
_ensure_up:
	@if [ -z "$(_container_running)" ]; then \
		echo "Container '$(CONTAINER_NAME)' is not running. Run: make -f dev.mk dev-up"; \
		exit 1; \
	fi

_ensure_redis:
	@docker exec $(CONTAINER_NAME) bash -c "sudo service redis-server start" >/dev/null 2>&1 || true

# ── Container lifecycle ────────────────────────────────────────────────────────
.PHONY: dev-up
dev-up:
	@if docker image inspect $(DEV_IMAGE) >/dev/null 2>&1; then \
		echo "Using local image $(DEV_IMAGE)"; \
	elif docker pull $(DEV_IMAGE) 2>/dev/null; then \
		echo "Pulled $(DEV_IMAGE)"; \
	else \
		echo "⚠️  Could not pull $(DEV_IMAGE) — building locally (this takes ~10 min on first run)."; \
		echo "   Requires: deps/ populated via 'make -f dev.mk fetch-deps' or the CI download-dependencies template."; \
		$(MAKE) -f dev.mk dev-image; \
	fi
	@if docker ps -a --format '{{.Names}}' | grep -qx "$(CONTAINER_NAME)"; then \
		if docker ps --format '{{.Names}}' | grep -qx "$(CONTAINER_NAME)"; then \
			echo "Container '$(CONTAINER_NAME)' is already running."; \
		else \
			echo "Starting existing container '$(CONTAINER_NAME)' ..."; \
			docker start $(CONTAINER_NAME) > /dev/null; \
		fi; \
	else \
		docker run -d \
			--name $(CONTAINER_NAME) \
			-v $(WORKSPACE):/workspace/sonic-gnmi \
			$(DEV_IMAGE) \
			sleep infinity; \
	fi
	@echo ""
	@echo "✅ Container '$(CONTAINER_NAME)' is running."
	@echo "   Run 'make -f dev.mk build' or 'make -f dev.mk test PKG=...'"

.PHONY: dev-down
dev-down:
	-docker stop $(CONTAINER_NAME)
	-docker rm $(CONTAINER_NAME)

.PHONY: shell
shell: _ensure_up _ensure_redis
	docker exec -it $(CONTAINER_NAME) bash

# ── Generate + vendor sync ─────────────────────────────────────────────────────
# Generates swsscommon SWIG bindings from the installed swsscommon.i, then
# syncs vendor/. Must run before any build or test that uses swsscommon CGO.
.PHONY: vendor
vendor: _ensure_up
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && make -C swsscommon && go mod vendor"
	@echo "✅ swsscommon bindings generated, vendor/ synced."

# ── Build ──────────────────────────────────────────────────────────────────────
.PHONY: build
build: vendor
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && \
		 ENABLE_TRANSLIB_WRITE=y ENABLE_NATIVE_WRITE=y \
		 dpkg-buildpackage -rfakeroot -us -uc -b -j\$$(nproc)"
	@echo "✅ Build complete. Debs are at the parent of your workspace."

# ── Tests ──────────────────────────────────────────────────────────────────────
# Run a single test or all tests in a package:
#   make -f dev.mk test PKG=gnmi_server TEST=TestServerUnixSocket
#   make -f dev.mk test PKG=sonic_db_config
.PHONY: test
test: vendor _ensure_up _ensure_redis
ifndef PKG
	$(error PKG is required. Example: make -f dev.mk test PKG=gnmi_server TEST=TestServerUnixSocket)
endif
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && \
		 export $(CGO_ENV) && \
		 $(if $(TEST), \
		   sudo -E go test -v $(GOMONKEY_FLAGS) -run '$(TEST)' github.com/sonic-net/sonic-gnmi/$(PKG), \
		   sudo -E go test -v $(GOMONKEY_FLAGS) github.com/sonic-net/sonic-gnmi/$(PKG))"

# Run the full integration test suite (slow — ~30 min)
.PHONY: test-all
test-all: vendor _ensure_up _ensure_redis
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && \
		 make all && ENABLE_TRANSLIB_WRITE=y make check_gotest"

# Pure package tests (fast, no SONiC deps needed)
.PHONY: test-pure
test-pure: _ensure_up
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && make -f pure.mk junit-xml"

# ── Build dev image locally ────────────────────────────────────────────────────
# Place pre-downloaded deps/ (libyang, libnl, swsscommon, yang_models wheel)
# in this directory before running dev-image. The Azure pipeline uses
# .azure/templates/download-dependencies.yml to populate deps/ automatically.
.PHONY: dev-image
dev-image:
	docker build \
		--build-arg BUILD_BRANCH=$(BUILD_BRANCH) \
		-f Dockerfile.dev \
		-t $(DEV_IMAGE) \
		.
	@echo "✅ Dev image built: $(DEV_IMAGE)"

.PHONY: dev-image-push
dev-image-push: dev-image
	docker push $(DEV_IMAGE)

# ── Help ───────────────────────────────────────────────────────────────────────
.PHONY: help
help:
	@echo ""
	@echo "sonic-gnmi dev workflow (dev.mk)"
	@echo ""
	@echo "  make -f dev.mk dev-up                              Pull pre-built image from ACR + start container"
	@echo "  make -f dev.mk dev-down                            Stop + remove container"
	@echo "  make -f dev.mk shell                               Interactive shell"
	@echo "  make -f dev.mk build                               Build sonic-gnmi .deb"
	@echo "  make -f dev.mk test PKG=<pkg> [TEST=<pattern>]     Run test(s) in a package"
	@echo "  make -f dev.mk test-all                            Full integration suite"
	@echo "  make -f dev.mk test-pure                           Fast pure-package tests"
	@echo "  make -f dev.mk dev-image [BUILD_BRANCH=202412]     Build dev image locally"
	@echo ""
	@echo "Packages: gnmi_server  telemetry  sonic_db_config  dialout  ..."
	@echo ""
