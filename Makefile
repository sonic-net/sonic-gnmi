ifeq ($(GOPATH),)
export GOPATH=/tmp/go
endif
export PATH := $(PATH):$(GOPATH)/bin

INSTALL := /usr/bin/install
DBDIR := /var/run/redis/sonic-db/
GO := /usr/local/go/bin/go
TOP_DIR := $(abspath ..)
GO_MGMT_PATH=$(TOP_DIR)/sonic-mgmt-framework
BUILD_DIR := $(GOPATH)/bin
export CVL_SCHEMA_PATH := $(GO_MGMT_PATH)/src/cvl/schema

SRC_FILES=$(shell find . -name '*.go' | grep -v '_test.go' | grep -v '/tests/')
TEST_FILES=$(wildcard *_test.go)
TELEMETRY_TEST_DIR = $(GO_MGMT_PATH)/build/tests/gnmi_server
TELEMETRY_TEST_BIN = $(TELEMETRY_TEST_DIR)/server.test
ifeq ($(SONIC_TELEMETRY_READWRITE),y)
BLD_FLAGS := -tags readwrite
endif

.phony: mgmt-deps

all: sonic-telemetry $(TELEMETRY_TEST_BIN)

go.mod:
	/usr/local/go/bin/go mod init github.com/Azure/sonic-telemetry
mgmt-deps:
	rm -rf cvl
	rm -rf translib
	cp -r ../sonic-mgmt-framework/src/cvl ./
	cp -r ../sonic-mgmt-framework/src/translib ./
	find cvl -name \*\.go -exec sed -i -e 's/\"translib/\"github.com\/Azure\/sonic-telemetry\/translib/g' {} \;
	find translib -name \*\.go -exec sed -i -e 's/\"translib/\"github.com\/Azure\/sonic-telemetry\/translib/g' {} \;
	find cvl -name \*\.go -exec sed -i -e 's/\"cvl/\"github.com\/Azure\/sonic-telemetry\/cvl/g' {} \;
	find translib -name \*\.go -exec sed -i -e 's/\"cvl/\"github.com\/Azure\/sonic-telemetry\/cvl/g' {} \;
	sed -i -e 's/\.\.\/\.\.\/\.\.\/models\/yang/\.\.\/\.\.\/\.\.\/sonic-mgmt-framework\/models\/yang/' translib/ocbinds/oc.go
	sed -i -e 's/\$$GO run \$$BUILD_GOPATH\/src\/github.com\/openconfig\/ygot\/generator\/generator.go/generator/' translib/ocbinds/oc.go
	$(GO) get github.com/openconfig/gnmi@89b2bf29312cda887da916d0f3a32c1624b7935f
	$(GO) get github.com/openconfig/ygot@724a6b18a9224343ef04fe49199dfb6020ce132a
	$(GO) get github.com/openconfig/goyang@064f9690516f4f72db189f4690b84622c13b7296
	$(GO) get github.com/openconfig/goyang@064f9690516f4f72db189f4690b84622c13b7296
	$(GO) get golang.org/x/crypto/ssh/terminal@e9b2fee46413
	$(GO) get github.com/jipanyang/gnxi@f0a90cca6fd0041625bcce561b71f849c9b65a8d
	$(GO) install github.com/openconfig/ygot/generator
	$(GO) get -x github.com/golang/glog@23def4e6c14b4da8ac2ed8007337bc5eb5007998
	rm -rf vendor
	$(GO) mod vendor
	ln -s vendor src
	cp -r $(GOPATH)/pkg/mod/github.com/openconfig/gnmi@v0.0.0-20190823184014-89b2bf29312c/* vendor/github.com/openconfig/gnmi/
	cp -r $(GOPATH)/pkg/mod/github.com/openconfig/goyang@v0.0.0-20190924211109-064f9690516f/* vendor/github.com/openconfig/goyang/
	cp -r $(GOPATH)/pkg/mod/github.com/openconfig/ygot@v0.6.1-0.20190723223108-724a6b18a922/* vendor/github.com/openconfig/ygot/
	cp -r $(GOPATH)/pkg/mod/golang.org/x/crypto@v0.0.0-20191206172530-e9b2fee46413 vendor/golang.org/x/crypto
	chmod -R u+w vendor
	patch -d vendor -p0 <patches/gnmi_cli.all.patch
	patch -d vendor/github.com/antchfx/jsonquery -p1 < ../sonic-mgmt-framework/patches/jsonquery.patch
	patch -d vendor/github.com/openconfig/goyang -p1 < ../sonic-mgmt-framework/goyang-modified-files/goyang.patch
	patch -d vendor/github.com/openconfig -p1 < ../sonic-mgmt-framework/ygot-modified-files/ygot.patch
	$(GO) generate github.com/Azure/sonic-telemetry/translib/ocbinds
	make -C $(GO_MGMT_PATH)/src/cvl/schema
	make -C $(GO_MGMT_PATH)/models
	make -C $(GO_MGMT_PATH)/models/yang
	make -C $(GO_MGMT_PATH)/models/yang/sonic

sonic-telemetry: go.mod mgmt-deps
	$(GO) install -mod=vendor $(BLD_FLAGS) github.com/Azure/sonic-telemetry/telemetry
	$(GO) install -mod=vendor github.com/Azure/sonic-telemetry/dialout/dialout_client_cli
	$(GO) install github.com/jipanyang/gnxi/gnmi_get
	$(GO) install github.com/jipanyang/gnxi/gnmi_set
	$(GO) install -mod=vendor github.com/openconfig/gnmi/cmd/gnmi_cli

check:
	sudo mkdir -p ${DBDIR}
	sudo cp ./testdata/database_config.json ${DBDIR}
	sudo mkdir -p /usr/models/yang || true
	sudo find $(GO_MGMT_PATH)/models -name '*.yang' -exec cp {} /usr/models/yang/ \;
	-$(GO) test -mod=vendor -v github.com/Azure/sonic-telemetry/gnmi_server
	-$(GO) test -mod=vendor -v github.com/Azure/sonic-telemetry/dialout/dialout_client

clean:
	rm -rf cvl
	rm -rf translib
	rm -rf vendor
	chmod -f -R u+w $(GOPATH)/pkg || true
	rm -rf $(GOPATH)
	rm -f src

$(TELEMETRY_TEST_BIN): $(TEST_FILES) $(SRC_FILES)
	$(GO) test -mod=vendor -c -cover github.com/Azure/sonic-telemetry/gnmi_server -o $@
	cp -r testdata $(TELEMETRY_TEST_DIR)
	cp -r $(GO_MGMT_PATH)/src/cvl/schema $(TELEMETRY_TEST_DIR)

install:
	$(INSTALL) -D $(BUILD_DIR)/telemetry $(DESTDIR)/usr/sbin/telemetry
	$(INSTALL) -D $(BUILD_DIR)/dialout_client_cli $(DESTDIR)/usr/sbin/dialout_client_cli
	$(INSTALL) -D $(BUILD_DIR)/gnmi_get $(DESTDIR)/usr/sbin/gnmi_get
	$(INSTALL) -D $(BUILD_DIR)/gnmi_set $(DESTDIR)/usr/sbin/gnmi_set
	$(INSTALL) -D $(BUILD_DIR)/gnmi_cli $(DESTDIR)/usr/sbin/gnmi_cli

	mkdir -p $(DESTDIR)/usr/bin/
	cp -r $(GO_MGMT_PATH)/src/cvl/schema $(DESTDIR)/usr/sbin
	mkdir -p $(DESTDIR)/usr/models/yang
	find $(GO_MGMT_PATH)/models -name '*.yang' -exec cp {} $(DESTDIR)/usr/models/yang/ \;

deinstall:
	rm $(DESTDIR)/usr/sbin/telemetry
	rm $(DESTDIR)/usr/sbin/dialout_client_cli
	rm $(DESTDIR)/usr/sbin/gnmi_get
	rm $(DESTDIR)/usr/sbin/gnmi_set


