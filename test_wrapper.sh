#\!/bin/bash
sudo CGO_LDFLAGS='-lswsscommon -lhiredis -fsanitize=address' CGO_CXXFLAGS='-I/usr/include/swss -w -Wall -fpermissive -fsanitize=leak' /usr/local/go/bin/go test $@
