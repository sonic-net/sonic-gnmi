module github.com/Azure/sonic-telemetry

go 1.12

require (
	github.com/Azure/sonic-mgmt-common v0.0.0-00010101000000-000000000000
	github.com/Workiva/go-datastructures v1.0.50
	github.com/c9s/goprocinfo v0.0.0-20191125144613-4acdd056c72d
	github.com/go-redis/redis v6.15.6+incompatible
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/protobuf v1.3.2
	github.com/google/gnxi v0.0.0-20191016182648-6697a080bc2d
	github.com/jipanyang/gnmi v0.0.0-20180820232453-cb4d464fa018
	github.com/jipanyang/gnxi v0.0.0-20181221084354-f0a90cca6fd0 // indirect
	github.com/kylelemons/godebug v1.1.0
	github.com/onsi/ginkgo v1.10.3 // indirect
	github.com/onsi/gomega v1.7.1 // indirect
	github.com/openconfig/gnmi v0.0.0-20190823184014-89b2bf29312c
	github.com/openconfig/ygot v0.6.1-0.20190723223108-724a6b18a922
	github.com/stretchr/testify v1.4.0 // indirect
	golang.org/x/crypto v0.0.0-20191206172530-e9b2fee46413 // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/sys v0.0.0-20190412213103-97732733099d // indirect
	google.golang.org/grpc v1.25.1
)

replace github.com/Azure/sonic-mgmt-common => ../sonic-mgmt-common
