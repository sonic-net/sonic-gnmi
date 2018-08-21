ifeq ($(GOPATH),)
export GOPATH=/tmp/go
endif

INSTALL := /usr/bin/install

all: sonic-telemetry

sonic-telemetry:
	/usr/local/go/bin/go get -v github.com/Azure/sonic-telemetry/telemetry
	/usr/local/go/bin/go get -v github.com/Azure/sonic-telemetry/dialout/dialout_client_cli

install:
	$(INSTALL) -D ${GOPATH}/bin/telemetry $(DESTDIR)/usr/sbin/telemetry
	$(INSTALL) -D ${GOPATH}/bin/dialout_client_cli $(DESTDIR)/usr/sbin/dialout_client_cli

deinstall:
	rm $(DESTDIR)/usr/sbin/telemetry
	rm $(DESTDIR)/usr/sbin/dialout_client_cli

clean:
	rm -fr ${GOPATH}

