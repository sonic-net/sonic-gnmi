module github.com/Azure/sonic-telemetry

go 1.12

require (
	github.com/Workiva/go-datastructures v1.0.50
	github.com/antchfx/jsonquery v1.1.0
	github.com/antchfx/xmlquery v1.2.1
	github.com/antchfx/xpath v1.1.2 // indirect
	github.com/c9s/goprocinfo v0.0.0-20191125144613-4acdd056c72d
	github.com/go-redis/redis v6.15.6+incompatible
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/groupcache v0.0.0-20191027212112-611e8accdfc9 // indirect
	github.com/golang/protobuf v1.3.2
	github.com/google/gnxi v0.0.0-20191016182648-6697a080bc2d
	github.com/jipanyang/gnmi v0.0.0-20180820232453-cb4d464fa018
	github.com/jipanyang/gnxi v0.0.0-20181221084354-f0a90cca6fd0 // indirect
	github.com/kylelemons/godebug v1.1.0
	github.com/onsi/ginkgo v1.10.3 // indirect
	github.com/onsi/gomega v1.7.1 // indirect
	github.com/openconfig/gnmi v0.0.0-20190823184014-89b2bf29312c
	github.com/openconfig/goyang v0.0.0-20190924211109-064f9690516f
	github.com/openconfig/ygot v0.6.1-0.20190723223108-724a6b18a922
	github.com/pborman/getopt v0.0.0-20190409184431-ee0cd42419d3 // indirect
	github.com/stretchr/testify v1.4.0 // indirect
	golang.org/x/crypto v0.0.0-20191206172530-e9b2fee46413 // indirect
	golang.org/x/lint v0.0.0-20190313153728-d0100b6bd8b3 // indirect
	golang.org/x/net v0.0.0-20191209160850-c0dbc17a3553
	golang.org/x/text v0.3.0
	golang.org/x/tools v0.0.0-20190524140312-2c0ae7006135 // indirect
	google.golang.org/grpc v1.25.1
	honnef.co/go/tools v0.0.0-20190523083050-ea95bdfd59fc // indirect
)

replace github.com/Azure/sonic-mgmt-framework => ../sonic-mgmt-framework
