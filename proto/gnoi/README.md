export PATH=$PATH:$HOME/go/bin
protoc -I . -I ~/go/src --gofast_out=plugins=grpc:. *.proto
