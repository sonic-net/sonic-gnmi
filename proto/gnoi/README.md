# Compiling gRPC Proto Files

Follow these steps to compile the gRPC proto files:

1. **Install grpc proto compiler:**
    ```sh
    go install -mod=mod github.com/gogo/protobuf/protoc-gen-gofast
    ```

2. **Include Go path in your `PATH`:**
    ```sh
    export PATH="$PATH:$(go env GOPATH)/bin"
    ```

3. **Navigate to the `proto/gnoi` directory:**
    ```sh
    cd proto/gnoi
    ```

4. **Compile your proto file:**
    ```sh
    protoc -I . --gofast_out=plugins=grpc:. sonic_upgrade.proto
    ```