module github.com/Azure/sonic-telemetry

go 1.12

require (
	github.com/Azure/sonic-mgmt-common v0.0.0-00010101000000-000000000000
	github.com/Workiva/go-datastructures v1.0.50
	github.com/antchfx/jsonquery v1.1.0 // indirect
	github.com/antchfx/xmlquery v1.2.1 // indirect
	github.com/antchfx/xpath v1.1.2 // indirect
	github.com/c9s/goprocinfo v0.0.0-20191125144613-4acdd056c72d
	github.com/cenkalti/backoff/v4 v4.0.0 // indirect
	github.com/go-redis/redis v6.15.6+incompatible
	github.com/go-redis/redis/v7 v7.0.0-beta.3.0.20190824101152-d19aba07b476 // indirect
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/golang/protobuf v1.4.0-rc.4.0.20200313231945-b860323f09d0
	github.com/google/gnxi v0.0.0-20191016182648-6697a080bc2d
	github.com/google/protobuf v3.11.4+incompatible // indirect
	github.com/jipanyang/gnmi v0.0.0-20180820232453-cb4d464fa018
	github.com/jipanyang/gnxi v0.0.0-20181221084354-f0a90cca6fd0 // indirect
	github.com/kylelemons/godebug v1.1.0
	github.com/onsi/ginkgo v1.10.3 // indirect
	github.com/onsi/gomega v1.7.1 // indirect
	github.com/openconfig/gnmi v0.0.0-20200617225440-d2b4e6a45802
	github.com/openconfig/goyang v0.0.0-20200309174518-a00bece872fc // indirect
	github.com/openconfig/ygot v0.7.1
	github.com/pborman/getopt v0.0.0-20190409184431-ee0cd42419d3 // indirect
	github.com/stretchr/testify v1.4.0 // indirect
	golang.org/x/net v0.0.0-20200301022130-244492dfa37a
	golang.org/x/sys v0.0.0-20190412213103-97732733099d // indirect
	google.golang.org/genproto v0.0.0-20200319113533-08878b785e9c // indirect
	google.golang.org/grpc v1.28.0
	google.golang.org/protobuf v1.21.0 // indirect
)

replace github.com/Azure/sonic-mgmt-common => ../sonic-mgmt-common
