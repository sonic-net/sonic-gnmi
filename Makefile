ifeq ($(GOPATH),)
export GOPATH=/tmp/go
endif
export PATH := $(PATH):$(GOPATH)/bin

INSTALL := /usr/bin/install
DBDIR := /var/run/redis/sonic-db/
GO ?= /usr/local/go/bin/go
TOPDIR := $(abspath .)
MGMT_COMMON_DIR := $(TOPDIR)/../sonic-mgmt-common
BUILD_BASE := build
BUILD_DIR := build/bin
BUILD_GNOI_YANG_DIR := $(BUILD_BASE)/gnoi_yang
BUILD_GNOI_YANG_PROTO_DIR := $(BUILD_GNOI_YANG_DIR)/proto
BUILD_GNOI_YANG_SERVER_DIR := $(BUILD_GNOI_YANG_DIR)/server
BUILD_GNOI_YANG_CLIENT_DIR := $(BUILD_GNOI_YANG_DIR)/client
GNOI_YANG := $(BUILD_GNOI_YANG_PROTO_DIR)/.gnoi_yang_done
TOOLS_DIR        := $(TOPDIR)/tools
PYANG_PLUGIN_DIR := $(TOOLS_DIR)/pyang_plugins
PYANG  ?= pyang
GOROOT ?= $(shell $(GO) env GOROOT)
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

ifeq ($(ENABLE_TRANSLIB_WRITE),y)
BLD_TAGS := gnmi_translib_write
endif
ifeq ($(ENABLE_NATIVE_WRITE),y)
BLD_TAGS := $(BLD_TAGS) gnmi_native_write
endif

ifneq ($(BLD_TAGS),)
BLD_FLAGS := -tags "$(strip $(BLD_TAGS))"
endif

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
	$(GO) mod vendor
	$(GO) mod download github.com/google/gnxi@v0.0.0-20181220173256-89f51f0ce1e2
	cp -r $(GOPATH)/pkg/mod/github.com/google/gnxi@v0.0.0-20181220173256-89f51f0ce1e2/* vendor/github.com/google/gnxi/

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

# download and apply patch for gnmi client, which will break advancetls
# backup crypto and gnxi
	mkdir -p backup_crypto
	cp -r vendor/golang.org/x/crypto/* backup_crypto/

# download and patch crypto and gnxi
	$(GO) mod download golang.org/x/crypto@v0.0.0-20191206172530-e9b2fee46413
	cp -r $(GOPATH)/pkg/mod/golang.org/x/crypto@v0.0.0-20191206172530-e9b2fee46413/* vendor/golang.org/x/crypto/
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

# restore old version
	rm -rf vendor/golang.org/x/crypto/
	mv backup_crypto/ vendor/golang.org/x/crypto/

swsscommon_wrap:
	make -C swsscommon

.SECONDEXPANSION:

PROTOC_PATH := $(PATH):$(GOBIN)
PROTOC_OPTS := -I$(CURDIR)/vendor -I/usr/local/include -I/usr/include
PROTOC_OPTS_WITHOUT_VENDOR := -I/usr/local/include -I/usr/include

# Generate following go & grpc bindings using teh legacy protoc-gen-go
PROTO_GO_BINDINGS += proto/sonic_internal.pb.go
PROTO_GO_BINDINGS += proto/gnoi/sonic_debug.pb.go

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

$(GNOI_YANG): $(BUILD_GNOI_YANG_PROTO_DIR)/.proto_api_done $(BUILD_GNOI_YANG_PROTO_DIR)/.proto_sonic_done
	@echo "+++++ Compiling PROTOBUF files; +++++"
	$(GO) install github.com/gogo/protobuf/protoc-gen-gofast
	@mkdir -p $(@D)
	$(foreach file, $(wildcard $(BUILD_GNOI_YANG_PROTO_DIR)/*/*.proto), PATH=$(PROTOC_PATH) protoc -I$(@D) $(PROTOC_OPTS_WITHOUT_VENDOR) --gofast_out=plugins=grpc,Mgoogle/protobuf/struct.proto=github.com/gogo/protobuf/types:$(BUILD_GNOI_YANG_PROTO_DIR) $(file);)
	@echo "+++++ PROTOBUF completion completed; +++++"
	touch $@

$(PROTO_GO_BINDINGS): $$(patsubst %.pb.go,%.proto,$$@) | $(GOBIN)/protoc-gen-go
	PATH=$(PROTOC_PATH) protoc -I$(@D) $(PROTOC_OPTS) --go_out=plugins=grpc:$(@D) $<

$(GOBIN)/protoc-gen-go:
	cd $$(mktemp -d) && \
	$(GO) mod init protoc && \
	$(GO) install github.com/golang/protobuf/protoc-gen-go


DBCONFG = $(DBDIR)/database_config.json
ENVFILE = build/test/env.txt
TESTENV = $(shell cat $(ENVFILE))

$(DBCONFG): testdata/database_config.json
	sudo mkdir -p ${DBDIR}
	sudo cp ./testdata/database_config.json ${DBDIR}

$(ENVFILE):
	mkdir -p $(@D)
	tools/test/env.sh | grep -v DB_CONFIG_PATH | tee $@

check_gotest: $(DBCONFG) $(ENVFILE)
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race -coverprofile=coverage-telemetry.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/telemetry
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race -coverprofile=coverage-config.txt -covermode=atomic -v github.com/sonic-net/sonic-gnmi/sonic_db_config
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race -timeout 20m -coverprofile=coverage-gnmi.txt -covermode=atomic -mod=vendor $(BLD_FLAGS) -v github.com/sonic-net/sonic-gnmi/gnmi_server -coverpkg ../...
ifneq ($(ENABLE_DIALOUT_VALUE),0)
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -coverprofile=coverage-dialout.txt -covermode=atomic -mod=vendor $(BLD_FLAGS) -v github.com/sonic-net/sonic-gnmi/dialout/dialout_client
endif
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race -coverprofile=coverage-data.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/sonic_data_client
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(GO) test -race -coverprofile=coverage-dbus.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/sonic_service_client
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race -coverprofile=coverage-translutils.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/transl_utils
	sudo CGO_LDFLAGS="$(CGO_LDFLAGS)" CGO_CXXFLAGS="$(CGO_CXXFLAGS)" $(TESTENV) $(GO) test -race -coverprofile=coverage-gnoi-client-system.txt -covermode=atomic -mod=vendor -v github.com/sonic-net/sonic-gnmi/gnoi_client/system

	# Install required coverage tools
	$(GO) install github.com/axw/gocov/gocov@v1.1.0
	$(GO) install github.com/AlekSi/gocov-xml@latest
	$(GO) mod vendor

	# Filter out "mocks" and generated "proto" files from the coverage reports
	for file in coverage-*.txt; do grep -v -e "/mocks/" -e "proto/" $$file > $$file.filtered; done

	# Convert and generate the final coverage.xml file
	gocov convert coverage-*.txt.filtered | gocov-xml -source $(shell pwd) > coverage.xml

	# Cleanup temporary files
	rm -rf coverage-*.txt coverage-*.txt.filtered


check_memleak: $(DBCONFG) $(ENVFILE)
	sudo CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" $(GO) test -coverprofile=coverage-telemetry.txt -covermode=atomic -mod=vendor $(MEMCHECK_FLAGS) -v github.com/sonic-net/sonic-gnmi/telemetry
	sudo CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" $(GO) test -coverprofile=coverage-config.txt -covermode=atomic $(MEMCHECK_FLAGS) -v github.com/sonic-net/sonic-gnmi/sonic_db_config
	sudo CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" $(GO) test -coverprofile=coverage-gnmi.txt -covermode=atomic -mod=vendor $(MEMCHECK_FLAGS) -v github.com/sonic-net/sonic-gnmi/gnmi_server -coverpkg ../... -run TestGNMINative
	sudo CGO_LDFLAGS="$(MEMCHECK_CGO_LDFLAGS)" CGO_CXXFLAGS="$(MEMCHECK_CGO_CXXFLAGS)" $(GO) test -coverprofile=coverage-data.txt -covermode=atomic -mod=vendor $(MEMCHECK_FLAGS) -v github.com/sonic-net/sonic-gnmi/sonic_data_client


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


