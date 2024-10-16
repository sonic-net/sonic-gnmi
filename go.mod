module github.com/sonic-net/sonic-gnmi

go 1.15

require (
	github.com/Azure/sonic-mgmt-common v0.0.0-00010101000000-000000000000
	github.com/Workiva/go-datastructures v1.0.50
	github.com/agiledragon/gomonkey/v2 v2.8.0
	github.com/c9s/goprocinfo v0.0.0-20191125144613-4acdd056c72d
	github.com/dgrijalva/jwt-go v3.2.1-0.20210802184156-9742bd7fca1c+incompatible
	github.com/fsnotify/fsnotify v1.4.7
	github.com/go-redis/redis v6.15.6+incompatible
	github.com/godbus/dbus/v5 v5.1.0
	github.com/gogo/protobuf v1.3.2
	github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
	github.com/golang/protobuf v1.4.3
	github.com/google/gnxi v0.0.0-20191016182648-6697a080bc2d
	github.com/jipanyang/gnmi v0.0.0-20180820232453-cb4d464fa018
	github.com/jipanyang/gnxi v0.0.0-20181221084354-f0a90cca6fd0
	github.com/kylelemons/godebug v1.1.0
	github.com/msteinert/pam v0.0.0-20201130170657-e61372126161
	github.com/onsi/ginkgo v1.10.3 // indirect
	github.com/onsi/gomega v1.7.1 // indirect
	github.com/openconfig/gnmi v0.0.0-20200617225440-d2b4e6a45802
	github.com/openconfig/gnoi v0.0.0-20201210212451-209899112bb7
	github.com/openconfig/ygot v0.7.1
	github.com/stretchr/testify v1.4.0 // indirect
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9
	golang.org/x/net v0.0.0-20201110031124-69a78807bb2b
	google.golang.org/grpc v1.33.2
	gopkg.in/yaml.v2 v2.2.8
)

replace github.com/Azure/sonic-mgmt-common => ../sonic-mgmt-common

replace github.com/openconfig/gnoi => github.com/openconfig/gnoi v0.0.0-20201210212451-209899112bb7
