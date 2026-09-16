INSTALL := /usr/bin/install
DBDIR := /var/run/redis/sonic-db/
GO ?= /usr/local/go/bin/go
TOPDIR := $(abspath .)
MGMT_COMMON_DIR := $(TOPDIR)/../sonic-mgmt-common
BUILD_BASE := build
BUILD_DIR := build/bin
TOOLS_BIN_DIR := $(BUILD_BASE)/tools/bin
BUILD_GNOI_YANG_DIR := $(BUILD_BASE)/gnoi_yang
BUILD_GNOI_YANG_PROTO_DIR := $(BUILD_GNOI_YANG_DIR)/proto
BUILD_GNOI_YANG_SERVER_DIR := $(BUILD_GNOI_YANG_DIR)/server
BUILD_GNOI_YANG_CLIENT_DIR := $(BUILD_GNOI_YANG_DIR)/client
GNOI_YANG := $(BUILD_GNOI_YANG_PROTO_DIR)/.gnoi_yang_done
TOOLS_DIR        := $(TOPDIR)/tools
PYANG_PLUGIN_DIR := $(TOOLS_DIR)/pyang_plugins
PYANG  ?= pyang
GOROOT ?= $(shell $(GO) env GOROOT)
HOST_GOOS = $(shell $(GO) env GOHOSTOS)
HOST_GOARCH = $(shell $(GO) env GOHOSTARCH)
HOST_GO_ENV = GOOS=$(HOST_GOOS) GOARCH=$(HOST_GOARCH)
GO_BIN_DIR = $(dir $(shell command -v $(GO)))
SUDO_GO := sudo -H env -i HOME=/root USER=root LOGNAME=root \
	PATH=$(GO_BIN_DIR):/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
FORMAT_CHECK = $(BUILD_DIR)/.formatcheck
FORMAT_LOG = $(BUILD_DIR)/go_format.log
# Find all .go files excluding vendor, build, and patches files
GO_FILES := $(shell find . -type f -name '*.go' ! -path './vendor/*' ! -path './build/*' ! -path './patches/*' ! -path './proto/*' ! -path './swsscommon/*')
export CVL_SCHEMA_PATH := $(MGMT_COMMON_DIR)/build/cvl/schema

API_YANGS=$(shell find $(MGMT_COMMON_DIR)/build/yang -name '*.yang' -not -path '*/sonic/*' -not -path '*/annotations/*')
SONIC_YANGS=$(shell find $(MGMT_COMMON_DIR)/models/yang/sonic -name '*.yang')

export GOBIN := $(abspath $(BUILD_DIR))
export PATH := $(PATH):$(GOBIN):$(shell dirname $(GO))
export CGO_LDFLAGS := -lswsscommon -lhiredis
export CGO_CXXFLAGS := -I/usr/include/swss -w -Wall -fpermissive
export MEMCHECK_CGO_LDFLAGS := $(CGO_LDFLAGS) -fsanitize=address
export MEMCHECK_CGO_CXXFLAGS := $(CGO_CXXFLAGS) -fsanitize=leak

GOTESTSUM := $(abspath $(TOOLS_BIN_DIR)/gotestsum-v1.13.0)
GOCOV := $(abspath $(TOOLS_BIN_DIR)/gocov-v1.1.0)
GOCOV_XML := $(abspath $(TOOLS_BIN_DIR)/gocov-xml-v1.2.0)
PROTOC_GEN_GO := $(abspath $(TOOLS_BIN_DIR)/protoc-gen-go-v1.5.4)
PROTOC_GEN_GOFAST := $(abspath $(TOOLS_BIN_DIR)/protoc-gen-gofast-v1.3.2)

ifeq ($(ENABLE_TRANSLIB_WRITE),y)
BLD_TAGS := gnmi_translib_write
endif
ifeq ($(ENABLE_NATIVE_WRITE),y)
BLD_TAGS := $(BLD_TAGS) gnmi_native_write
endif

ifneq ($(BLD_TAGS),)
BLD_FLAGS := -tags "$(strip $(BLD_TAGS))"
endif

# Disable function inlining so gomonkey can patch methods at runtime.
# Required for Go 1.24+ where the compiler aggressively inlines methods.
TEST_FLAGS := -gcflags=all=-l

MEMCHECK_TAGS := $(BLD_TAGS) gnmi_memcheck
ifneq ($(MEMCHECK_TAGS),)
MEMCHECK_FLAGS := -tags "$(strip $(MEMCHECK_TAGS))"
endif

ENABLE_DIALOUT_VALUE := 1
ifeq ($(ENABLE_DIALOUT),n)
ENABLE_DIALOUT_VALUE = 0
endif

GO_DEPS := vendor/.done
PATCHES := $(wildcard patches/*.patch)
PATCHES += $(shell find $(MGMT_COMMON_DIR)/patches -type f)

all: sonic-gnmi

go.mod:
	$(GO) mod init github.com/sonic-net/sonic-gnmi

$(GO_DEPS): go.mod $(PATCHES) swsscommon_wrap $(GNOI_YANG)
	@GO_MOD_VER=$$(sed -n 's/^go //p' go.mod) && \
	 GO_CUR_VER=$$($(GO) env GOVERSION | sed 's/go//') && \
	 if printf '%s\n' "$$GO_MOD_VER" "$$GO_CUR_VER" | sort -V | head -1 | grep -qx "$$GO_MOD_VER"; then \
	   $(GO) mod tidy; \
	 fi
	$(GO) mod vendor
	$(GO) mod download github.com/google/gnxi@v0.0.0-20181220173256-89f51f0ce1e2
	gnxi_dir=$$($(GO) list -mod=mod -m -f '{{.Dir}}' github.com/google/gnxi@v0.0.0-20181220173256-89f51f0ce1e2) && \
		cp -r "$$gnxi_dir"/* vendor/github.com/google/gnxi/ && \
		chmod -R u+w vendor/github.com/google/gnxi

# x/crypto/ssh/terminal imports x/term in v0.24.0+; gnmi_cli uses ssh/terminal
# but go mod vendor omits both because the unpatched code doesn't import them.
# Copy both explicitly so gnmi_cli can compile after patches are applied.
# Also copy cmd/gnmi_cli and cli from the pinned gnmi module (go mod vendor omits
# cmd packages that are not imported by any package in this module).
	$(GO) mod download golang.org/x/term@v0.43.0
	mkdir -p vendor/golang.org/x/crypto/ssh/terminal vendor/golang.org/x/term
	crypto_dir=$$($(GO) list -mod=mod -m -f '{{.Dir}}' golang.org/x/crypto@v0.52.0) && \
		cp "$$crypto_dir"/ssh/terminal/terminal.go vendor/golang.org/x/crypto/ssh/terminal/
	term_dir=$$($(GO) list -mod=mod -m -f '{{.Dir}}' golang.org/x/term@v0.43.0) && \
		rsync -r --chmod=u+w --exclude=testdata --exclude='*_test.go' \
		"$$term_dir"/ vendor/golang.org/x/term/
	python3 -c 'import re, sys; txt=open("vendor/modules.txt").read(); pkg="golang.org/x/term\n"; m=re.search(r"^(# golang\.org/x/term [^\n]+\n## explicit[^\n]*\n)", txt, re.MULTILINE) if pkg not in txt else None; sys.exit("ERROR: golang.org/x/term block not found in vendor/modules.txt") if pkg not in txt and not m else None; open("vendor/modules.txt","w").write(txt[:m.end()]+pkg+txt[m.end():]) if m else None'
	$(GO) mod download github.com/openconfig/gnmi@v0.0.0-20200617225440-d2b4e6a45802
	mkdir -p vendor/github.com/openconfig/gnmi/cmd/gnmi_cli \
		vendor/github.com/openconfig/gnmi/cli \
		vendor/github.com/openconfig/gnmi/client/flags
	gnmi_dir=$$($(GO) list -mod=mod -m -f '{{.Dir}}' github.com/openconfig/gnmi@v0.0.0-20200617225440-d2b4e6a45802) && \
		cp "$$gnmi_dir"/cmd/gnmi_cli/gnmi_cli.go vendor/github.com/openconfig/gnmi/cmd/gnmi_cli/ && \
		cp "$$gnmi_dir"/cli/cli.go vendor/github.com/openconfig/gnmi/cli/ && \
		cp "$$gnmi_dir"/client/flags/intmap.go \
			"$$gnmi_dir"/client/flags/stringlist.go \
			"$$gnmi_dir"/client/flags/stringmap.go \
			vendor/github.com/openconfig/gnmi/client/flags/

# Apply patch from sonic-mgmt-common, ignore glog.patch because glog version changed
	sed -i 's/patch -d $${DEST_DIR}\/github.com\/golang\/glog/\#patch -d $${DEST_DIR}\/github.com\/golang\/glog/g' $(MGMT_COMMON_DIR)/patches/apply.sh
	$(MGMT_COMMON_DIR)/patches/apply.sh vendor
	sed -i 's/#patch -d $${DEST_DIR}\/github.com\/golang\/glog/patch -d $${DEST_DIR}\/github.com\/golang\/glog/g' $(MGMT_COMMON_DIR)/patches/apply.sh

	chmod -R u+w vendor
	patch -d vendor -p0 < patches/gnmi_path.patch
	patch -d vendor -p0 < patches/gnmi_xpath.patch

	touch $@

go-deps: $(GO_DEPS)

go-deps-clean:
	$(RM) -r vendor

sonic-gnmi: $(GO_DEPS) $(FORMAT_CHECK)
# advancetls 1.0.0 release need following patch to build by go-1.19
	patch -d vendor -p0 < patches/0002-Fix-advance-tls-build-with-go-119.patch
# build service first which depends on advancetls
# add support for fsnotify closewrite event
	patch -d vendor -p0 < patches/0004-CloseWrite-event-support.patch
ifeq ($(CROSS_BUILD_ENVIRON),y)
	$(GO) build -o ${GOBIN}/telemetry -mod=vendor $(BLD_FLAGS) github.com/sonic-net/sonic-gnmi/telemetry
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	$(GO) build -o ${GOBIN}/dialout_client_cli -mod=vendor $(BLD_FLAGS) github.com/sonic-net/sonic-gnmi/dialout/dialout_client_cli
endif
	$(GO) build -o ${GOBIN}/gnoi_client -mod=vendor github.com/sonic-net/sonic-gnmi/gnoi_client
	$(GO) build -o ${GOBIN}/gnmi_dump -mod=vendor github.com/sonic-net/sonic-gnmi/gnmi_dump
else
	$(GO) install -mod=vendor $(BLD_FLAGS) github.com/sonic-net/sonic-gnmi/telemetry
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	$(GO) install -mod=vendor $(BLD_FLAGS) github.com/sonic-net/sonic-gnmi/dialout/dialout_client_cli
endif
	$(GO) install -mod=vendor github.com/sonic-net/sonic-gnmi/gnoi_client
	$(GO) install -mod=vendor github.com/sonic-net/sonic-gnmi/gnmi_dump
	$(GO) install -mod=vendor github.com/sonic-net/sonic-gnmi/build/gnoi_yang/client/gnoi_openconfig_client
	$(GO) install -mod=vendor github.com/sonic-net/sonic-gnmi/build/gnoi_yang/client/gnoi_sonic_client

endif

# download and apply patch for gnmi client
# use the already-vendored crypto (no longer need the old 2019 override;
# x/crypto v0.24.0+ retains ssh/terminal and RevokedCertificates backward compat)
	chmod -R u+w vendor
	patch -d vendor -p0 < patches/gnmi_cli.all.patch
	patch -d vendor -p0 < patches/gnmi_set.patch
	patch -d vendor -p0 < patches/gnmi_get.patch
	git apply patches/0001-Updated-to-filter-and-write-to-file.patch
	git apply patches/0003-Fix-client-json-parsing-issue.patch

# Manually adding patched client packages and their dependencies
# to vendor/modules.txt. This satisfies 'go install -mod=vendor' lookup checks,
# which are required after manual patching/copying of gnxi and gnmi-cli code.
	echo "github.com/google/gnxi v0.0.0-20181220173256-89f51f0ce1e2" >> vendor/modules.txt
	echo "github.com/google/gnxi/gnmi_get" >> vendor/modules.txt
	echo "github.com/google/gnxi/gnmi_set" >> vendor/modules.txt
	echo "github.com/openconfig/gnmi/cli" >> vendor/modules.txt
	echo "github.com/openconfig/gnmi/client/flags" >> vendor/modules.txt
	echo "golang.org/x/crypto/ssh/terminal" >> vendor/modules.txt
	echo "github.com/openconfig/gnmi/cmd/gnmi_cli" >> vendor/modules.txt

ifeq ($(CROSS_BUILD_ENVIRON),y)
	$(GO) build -o ${GOBIN}/gnmi_get -mod=vendor github.com/google/gnxi/gnmi_get
	$(GO) build -o ${GOBIN}/gnmi_set -mod=vendor github.com/google/gnxi/gnmi_set
	$(GO) build -o ${GOBIN}/gnmi_cli -mod=vendor github.com/openconfig/gnmi/cmd/gnmi_cli
else
	$(GO) install -mod=vendor github.com/google/gnxi/gnmi_get
	$(GO) install -mod=vendor github.com/google/gnxi/gnmi_set
	$(GO) install -mod=vendor github.com/openconfig/gnmi/cmd/gnmi_cli
endif

swsscommon_wrap:
	make -C swsscommon

.SECONDEXPANSION:

PROTOC_OPTS := -I$(CURDIR)/vendor -I/usr/local/include -I/usr/include
PROTOC_OPTS_WITHOUT_VENDOR := -I/usr/local/include -I/usr/include

# Generate following go & grpc bindings using teh legacy protoc-gen-go
PROTO_GO_BINDINGS += proto/sonic_internal.pb.go
PROTO_GO_BINDINGS += proto/gnoi/sonic_debug.pb.go
PROTO_GO_BINDINGS += proto/gnoi/oras/oras.pb.go

$(BUILD_GNOI_YANG_PROTO_DIR)/.proto_api_done: $(API_YANGS)
	@echo "+++++ Generating PROTOBUF files for API Yang modules; +++++"
	$(PYANG) \
		-f proto \
		--proto-outdir $(BUILD_GNOI_YANG_PROTO_DIR) \
		--plugindir $(PYANG_PLUGIN_DIR) \
		--server-rpc-outdir $(BUILD_GNOI_YANG_SERVER_DIR) \
		--client-rpc-outdir $(BUILD_GNOI_YANG_CLIENT_DIR) \
		-p $(MGMT_COMMON_DIR)/build/yang/common:$(MGMT_COMMON_DIR)/build/yang/extensions \
		$(MGMT_COMMON_DIR)/build/yang/*.yang $(MGMT_COMMON_DIR)/build/yang/extensions/*.yang
	@echo "+++++ Generation of protobuf files for API Yang modules completed +++++"
	touch $@

$(BUILD_GNOI_YANG_PROTO_DIR)/.proto_sonic_done: $(SONIC_YANGS)
	@echo "+++++ Generating PROTOBUF files for SONiC Yang modules; +++++"
	$(PYANG) \
		-f proto \
		--proto-outdir $(BUILD_GNOI_YANG_PROTO_DIR) \
		--plugindir $(PYANG_PLUGIN_DIR) \
		--server-rpc-outdir $(BUILD_GNOI_YANG_SERVER_DIR) \
		--client-rpc-outdir $(BUILD_GNOI_YANG_CLIENT_DIR) \
		-p $(MGMT_COMMON_DIR)/build/yang/common:$(MGMT_COMMON_DIR)/build/yang/sonic/common \
		$(MGMT_COMMON_DIR)/build/yang/sonic/*.yang
	@echo "+++++ Generation of protobuf files for SONiC Yang modules completed +++++"
	touch $@

$(GNOI_YANG): $(BUILD_GNOI_YANG_PROTO_DIR)/.proto_api_done $(BUILD_GNOI_YANG_PROTO_DIR)/.proto_sonic_done | $(PROTOC_GEN_GOFAST)
	@echo "+++++ Compiling PROTOBUF files; +++++"
	# Remove the toolchain directive added by newer Go versions
	sed -i '/^toolchain/d' go.mod
	@mkdir -p $(@D)
	$(foreach file, $(wildcard $(BUILD_GNOI_YANG_PROTO_DIR)/*/*.proto), protoc -I$(@D) $(PROTOC_OPTS_WITHOUT_VENDOR) --plugin=protoc-gen-gofast=$(PROTOC_GEN_GOFAST) --gofast_out=plugins=grpc,Mgoogle/protobuf/struct.proto=github.com/gogo/protobuf/types:$(BUILD_GNOI_YANG_PROTO_DIR) $(file);)
	@echo "+++++ PROTOBUF completion completed; +++++"
	touch $@

$(PROTO_GO_BINDINGS): $$(patsubst %.pb.go,%.proto,$$@) | $(PROTOC_GEN_GO)
	protoc -I$(@D) $(PROTOC_OPTS) --plugin=protoc-gen-go=$(PROTOC_GEN_GO) --go_out=plugins=grpc:$(@D) $<

$(TOOLS_BIN_DIR):
	mkdir -p $@

$(PROTOC_GEN_GOFAST): | $(TOOLS_BIN_DIR)
	$(HOST_GO_ENV) GOBIN=$(abspath $(TOOLS_BIN_DIR)) $(GO) install github.com/gogo/protobuf/protoc-gen-gofast@v1.3.2
	mv $(TOOLS_BIN_DIR)/protoc-gen-gofast $@

$(PROTOC_GEN_GO): | $(TOOLS_BIN_DIR)
	$(HOST_GO_ENV) GOBIN=$(abspath $(TOOLS_BIN_DIR)) $(GO) install github.com/golang/protobuf/protoc-gen-go@v1.5.4
	mv $(TOOLS_BIN_DIR)/protoc-gen-go $@

$(GOTESTSUM): | $(TOOLS_BIN_DIR)
	$(HOST_GO_ENV) GOBIN=$(abspath $(TOOLS_BIN_DIR)) $(GO) install gotest.tools/gotestsum@v1.13.0
	mv $(TOOLS_BIN_DIR)/gotestsum $@

$(GOCOV): | $(TOOLS_BIN_DIR)
	$(HOST_GO_ENV) GOBIN=$(abspath $(TOOLS_BIN_DIR)) $(GO) install github.com/axw/gocov/gocov@v1.1.0
	mv $(TOOLS_BIN_DIR)/gocov $@

$(GOCOV_XML): | $(TOOLS_BIN_DIR)
	$(HOST_GO_ENV) GOBIN=$(abspath $(TOOLS_BIN_DIR)) $(GO) install github.com/AlekSi/gocov-xml@v1.2.0
	mv $(TOOLS_BIN_DIR)/gocov-xml $@


DBCONFG = $(DBDIR)/database_config.json
ENVFILE = build/test/env.txt
TESTENV = $(shell cat $(ENVFILE))

$(DBCONFG): testdata/database_config.json
	sudo mkdir -p ${DBDIR}
	sudo cp ./testdata/database_config.json ${DBDIR}

$(ENVFILE):
	mkdir -p $(@D)
	tools/test/env.sh | grep -v DB_CONFIG_PATH | tee $@

check_gotest: $(DBCONFG) $(ENVFILE) $(GOCOV) $(GOCOV_XML)
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race $(TEST_FLAGS) -coverprofile=coverage-telemetry.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/telemetry
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race $(TEST_FLAGS) -coverprofile=coverage-config.txt -covermode=atomic -v github.com/sonic-net/sonic-gnmi/sonic_db_config
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race -timeout 40m $(TEST_FLAGS) -coverprofile=coverage-gnmi.txt -covermode=atomic -mod=vendor $(BLD_FLAGS) -v github.com/sonic-net/sonic-gnmi/gnmi_server -coverpkg ../...
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race -timeout 40m $(TEST_FLAGS) -coverprofile=coverage-pathz_authorizer.txt -covermode=atomic -mod=vendor $(BLD_FLAGS) -v github.com/sonic-net/sonic-gnmi/pathz_authorizer -coverpkg ../...
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test $(TEST_FLAGS) -coverprofile=coverage-dialout.txt -covermode=atomic -mod=vendor $(BLD_FLAGS) -v github.com/sonic-net/sonic-gnmi/dialout/dialout_client
endif
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race $(TEST_FLAGS) -coverprofile=coverage-data.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/sonic_data_client
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race $(TEST_FLAGS) -coverprofile=coverage-dbus.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/sonic_service_client
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race $(TEST_FLAGS) -coverprofile=coverage-translutils.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/transl_utils
	$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race $(TEST_FLAGS) -coverprofile=coverage-gnoi-client-system.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/gnoi_client/system

	# Filter out "mocks" and generated "proto" files from the coverage reports
	for file in coverage-*.txt; do grep -v -e "/mocks/" -e "proto/" $$file > $$file.filtered; done

	# Convert and generate the final coverage.xml file
	$(GOCOV) convert coverage-*.txt.filtered | $(GOCOV_XML) -source $(shell pwd) > coverage.xml

	# Cleanup temporary files
	rm -rf coverage-*.txt coverage-*.txt.filtered


# Integration test packages - basic ones (no special environment needed)
INTEGRATION_BASIC_PKGS := \
	github.com/sonic-net/sonic-gnmi/sonic_db_config \
	github.com/sonic-net/sonic-gnmi/sonic_service_client \
	github.com/sonic-net/sonic-gnmi/telemetry \
	github.com/sonic-net/sonic-gnmi/sonic_data_client

# Integration test packages that need special environment
INTEGRATION_ENV_PKGS := \
	github.com/sonic-net/sonic-gnmi/gnmi_server \
	github.com/sonic-net/sonic-gnmi/pathz_authorizer \
	github.com/sonic-net/sonic-gnmi/transl_utils \
	github.com/sonic-net/sonic-gnmi/gnoi_client/system

# Dialout package (conditional)
INTEGRATION_DIALOUT_PKG := \
	github.com/sonic-net/sonic-gnmi/dialout/dialout_client

# Memory leak test packages that require CGO/SONiC dependencies and special sanitizer flags
# Note: sonic_data_client and telemetry excluded due to underlying libyang memory leaks in test environment
MEMLEAK_STANDARD_PKGS := \
	github.com/sonic-net/sonic-gnmi/sonic_db_config

# gnmi_server has specific native tests for memory leak detection
# Note: Currently commented out due to libyang memory leaks that need to be fixed separately
# MEMLEAK_GNMI_SERVER_PKG := github.com/sonic-net/sonic-gnmi/gnmi_server
# MEMLEAK_TEST_PATTERN := ^TestGNMINative

check_memleak: $(DBCONFG) $(ENVFILE)
	$(SUDO_GO) CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" $(GO) test -mod=vendor $(TEST_FLAGS) $(MEMCHECK_FLAGS) -v $(MEMLEAK_STANDARD_PKGS)
	# sudo CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" $(GO) test -mod=vendor $(MEMCHECK_FLAGS) -v $(MEMLEAK_GNMI_SERVER_PKG) -run="$(MEMLEAK_TEST_PATTERN)"

# JUnit XML output for memory leak tests in Azure Pipelines
.PHONY: check_memleak_junit
check_memleak_junit: $(DBCONFG) $(ENVFILE) $(GOTESTSUM)
	@echo "Running memory leak tests with JUnit XML output..."
	@mkdir -p test-results
	$(SUDO_GO) CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" \
		$(GOTESTSUM) --junitfile test-results/junit-memleak-standard.xml \
		--format testname \
		-- -mod=vendor $(MEMCHECK_FLAGS) -v $(MEMLEAK_STANDARD_PKGS)
	@echo ""
	@echo "============================================="
	@echo "✅ Memory leak JUnit XML generation completed!"
	@echo "============================================="
	@echo "Files generated:"
	@echo "  - test-results/junit-memleak-standard.xml (Standard packages)"
	@echo ""
	@echo "Tested packages:"
	@for pkg in $(MEMLEAK_STANDARD_PKGS); do \
		echo "  - $$pkg"; \
	done
	@echo ""
	@echo "Note: telemetry, sonic_data_client, and gnmi_server excluded due to libyang memory leaks"


# JUnit XML output for integration tests in Azure Pipelines
.PHONY: check_gotest_junit
check_gotest_junit: $(DBCONFG) $(ENVFILE) $(GOTESTSUM) $(GOCOV) $(GOCOV_XML)
	@echo "Running integration tests with JUnit XML output..."
	@mkdir -p test-results
	
	# TODO: Fix tests to not depend on /etc/sonic existing
	# Creating directory here as workaround for poorly written tests
	sudo mkdir -p /etc/sonic && sudo chmod 777 /etc/sonic
	
	# Run basic packages (no special environment needed)
	@if [ -n "$(INTEGRATION_BASIC_PKGS)" ]; then \
		echo "Running basic integration tests..."; \
		$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" \
			$(GOTESTSUM) --junitfile test-results/junit-integration-basic.xml \
			--format testname \
			-- -race -timeout 40m $(TEST_FLAGS) -coverprofile=test-results/coverage-integration-basic.txt \
			-covermode=atomic -mod=vendor -v $(INTEGRATION_BASIC_PKGS); \
	fi
	
	# Run packages needing special environment
	@if [ -n "$(INTEGRATION_ENV_PKGS)" ]; then \
		echo "Pre-checking env packages compile..."; \
		$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) \
			$(GO) test -run=^$$ -mod=vendor $(BLD_FLAGS) $(INTEGRATION_ENV_PKGS) 2>&1 || true; \
		echo "Running environment-dependent integration tests..."; \
		$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) \
			$(GOTESTSUM) --junitfile test-results/junit-integration-env.xml \
			--format testname \
			-- -race -timeout 40m $(TEST_FLAGS) -coverprofile=test-results/coverage-integration-env.txt \
			-covermode=atomic -mod=vendor $(BLD_FLAGS) -v $(INTEGRATION_ENV_PKGS); \
	fi
	
	# Run dialout package if enabled
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	@if [ -n "$(INTEGRATION_DIALOUT_PKG)" ]; then \
		echo "Running dialout integration tests..."; \
		$(SUDO_GO) CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) \
			$(GOTESTSUM) --junitfile test-results/junit-integration-dialout.xml \
			--format testname \
			-- $(TEST_FLAGS) -coverprofile=test-results/coverage-integration-dialout.txt \
			-covermode=atomic -mod=vendor $(BLD_FLAGS) -v $(INTEGRATION_DIALOUT_PKG); \
	fi
endif

	@echo "Generating coverage.xml..."
	@for file in test-results/coverage-*.txt; do \
		if [ -f "$$file" ]; then \
			grep -v -e "/mocks/" -e "proto/" "$$file" > "$$file.filtered" 2>/dev/null || true; \
		fi; \
	done
	$(GOCOV) convert test-results/coverage-*.txt.filtered | $(GOCOV_XML) -source $(shell pwd) > coverage.xml
	@rm -f test-results/coverage-*.txt.filtered

	@echo ""
	@echo "============================================="
	@echo "Integration JUnit XML generation completed!"
	@echo "============================================="
	@echo "Files generated:"
	@echo "  - test-results/junit-integration-basic.xml (Basic packages)"
	@echo "  - test-results/junit-integration-env.xml (Environment-dependent packages)"
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	@echo "  - test-results/junit-integration-dialout.xml (Dialout package)"
endif
	@echo "  - coverage.xml (Cobertura coverage report)"
	@echo ""
	@echo "Tested packages:"
	@echo "Basic packages:"
	@for pkg in $(INTEGRATION_BASIC_PKGS); do echo "  - $$pkg"; done
	@echo "Environment-dependent packages:"
	@for pkg in $(INTEGRATION_ENV_PKGS); do echo "  - $$pkg"; done
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	@echo "Dialout package:"
	@for pkg in $(INTEGRATION_DIALOUT_PKG); do echo "  - $$pkg"; done
endif


clean:
	$(RM) -r build
	$(RM) -r vendor

# File target that generates a diff file if formatting is incorrect
$(FORMAT_CHECK): $(GO_FILES)
	@echo "Checking Go file formatting..."
	@echo $(GO_FILES)
	mkdir -p $(@D)
	@$(GOROOT)/bin/gofmt -l $(GO_FILES) > $(FORMAT_LOG)
	@if [ -s $(FORMAT_LOG) ]; then \
		cat $(FORMAT_LOG); \
		echo "Formatting issues found. Please run 'gofmt -w <file>' on the above files and commit the changes."; \
		exit 1; \
	else \
		echo "All files are properly formatted."; \
		rm -f $(FORMAT_LOG); \
	fi
	touch $@

install:
	$(INSTALL) -D $(BUILD_DIR)/telemetry $(DESTDIR)/usr/sbin/telemetry
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	$(INSTALL) -D $(BUILD_DIR)/dialout_client_cli $(DESTDIR)/usr/sbin/dialout_client_cli
endif
	$(INSTALL) -D $(BUILD_DIR)/gnmi_get $(DESTDIR)/usr/sbin/gnmi_get
	$(INSTALL) -D $(BUILD_DIR)/gnmi_set $(DESTDIR)/usr/sbin/gnmi_set
	$(INSTALL) -D $(BUILD_DIR)/gnmi_cli $(DESTDIR)/usr/sbin/gnmi_cli
	$(INSTALL) -D $(BUILD_DIR)/gnoi_client $(DESTDIR)/usr/sbin/gnoi_client
	$(INSTALL) -D $(BUILD_DIR)/gnoi_openconfig_client $(DESTDIR)/usr/sbin/gnoi_openconfig_client
	$(INSTALL) -D $(BUILD_DIR)/gnoi_sonic_client $(DESTDIR)/usr/sbin/gnoi_sonic_client
	$(INSTALL) -D $(BUILD_DIR)/gnmi_dump $(DESTDIR)/usr/sbin/gnmi_dump


deinstall:
	rm $(DESTDIR)/usr/sbin/telemetry
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	rm $(DESTDIR)/usr/sbin/dialout_client_cli
endif
	rm $(DESTDIR)/usr/sbin/gnmi_get
	rm $(DESTDIR)/usr/sbin/gnmi_set
	rm $(DESTDIR)/usr/sbin/gnoi_client
	rm $(DESTDIR)/usr/sbin/gnoi_openconfig_client
	rm $(DESTDIR)/usr/sbin/gnoi_sonic_client
	rm $(DESTDIR)/usr/sbin/gnmi_dump


TARGET_BRANCH ?= origin/master
DIFF_COVER_THRESHOLD ?= 80

.PHONY: diff-cover
diff-cover: coverage.xml test-results/coverage-pure.xml
	diff-cover coverage.xml test-results/coverage-pure.xml \
		--compare-branch $(TARGET_BRANCH) \
		--src-roots . \
		--fail-under $(DIFF_COVER_THRESHOLD)
