module gnoi_client/containerz

go 1.19

require (
	github.com/openconfig/gnoi v0.5.0
	github.com/sonic-net/sonic-gnmi v0.0.0
	google.golang.org/grpc v1.64.1
)

require (
	golang.org/x/net v0.26.0 // indirect
	golang.org/x/sys v0.26.0 // indirect
	golang.org/x/text v0.16.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250115164207-1a7da9e5054f // indirect
	google.golang.org/protobuf v1.36.3 // indirect
)

replace github.com/sonic-net/sonic-gnmi => ../..
