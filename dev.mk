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
DEV_IMAGE      ?= ghcr.io/sonic-net/sonic-gnmi/dev:latest
CONTAINER_NAME ?= sonic-gnmi-dev
WORKSPACE      := $(abspath .)
BUILD_BRANCH   ?= master

# CGO flags required for all go test invocations
CGO_ENV := CGO_LDFLAGS='-lswsscommon -lhiredis' \
           CGO_CXXFLAGS='-I/usr/include/swss -w -Wall -fpermissive'

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
	@echo "Pulling $(DEV_IMAGE) ..."
	docker pull $(DEV_IMAGE)
	docker run -d \
		--name $(CONTAINER_NAME) \
		-v $(WORKSPACE):/workspace/sonic-gnmi \
		$(DEV_IMAGE) \
		sleep infinity
	@echo ""
	@echo "✅ Container '$(CONTAINER_NAME)' is running."
	@echo "   Run 'make -f dev.mk build' or 'make -f dev.mk test PKG=...'"

.PHONY: dev-down
dev-down:
	docker stop $(CONTAINER_NAME) && docker rm $(CONTAINER_NAME) || true

.PHONY: shell
shell: _ensure_up _ensure_redis
	docker exec -it $(CONTAINER_NAME) bash

# ── Build ──────────────────────────────────────────────────────────────────────
.PHONY: build
build: _ensure_up
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
test: _ensure_up _ensure_redis
ifndef PKG
	$(error PKG is required. Example: make -f dev.mk test PKG=gnmi_server TEST=TestServerUnixSocket)
endif
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && \
		 export $(CGO_ENV) && \
		 $(if $(TEST), \
		   sudo -E go test -v -mod=vendor -run '$(TEST)' github.com/sonic-net/sonic-gnmi/$(PKG), \
		   sudo -E go test -v -mod=vendor github.com/sonic-net/sonic-gnmi/$(PKG))"

# Run the full integration test suite (slow — ~30 min)
.PHONY: test-all
test-all: _ensure_up _ensure_redis
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && \
		 make all && ENABLE_TRANSLIB_WRITE=y make check_gotest"

# Pure package tests (fast, no SONiC deps needed)
.PHONY: test-pure
test-pure: _ensure_up
	docker exec $(CONTAINER_NAME) bash -c \
		"cd /workspace/sonic-gnmi && make -f pure.mk junit-xml"

# ── Build dev image locally ────────────────────────────────────────────────────
# Useful when CI hasn't published the image yet, or for local iteration.
# Downloads all artifacts at build time — no manual steps required.
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
	@echo "  make -f dev.mk dev-up                              Pull image + start container"
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
