module github.com/sonic-net/sonic-gnmi

go 1.21

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
	github.com/golang/glog v1.1.0
	github.com/golang/protobuf v1.5.3
	github.com/google/gnxi v0.0.0-20191016182648-6697a080bc2d
	github.com/jipanyang/gnmi v0.0.0-20180820232453-cb4d464fa018
	github.com/jipanyang/gnxi v0.0.0-20181221084354-f0a90cca6fd0
	github.com/kylelemons/godebug v1.1.0
	github.com/msteinert/pam v0.0.0-20201130170657-e61372126161
	github.com/openconfig/gnmi v0.0.0-20200617225440-d2b4e6a45802
	github.com/openconfig/gnoi v0.3.0
	github.com/openconfig/ygot v0.7.1
	golang.org/x/crypto v0.0.0-20211108221036-ceb1ce70b4fa
	golang.org/x/net v0.9.0
	google.golang.org/grpc v1.56.3
	google.golang.org/protobuf v1.30.0
	gopkg.in/yaml.v2 v2.2.8
)

require (
	github.com/antchfx/jsonquery v1.1.4 // indirect
	github.com/antchfx/xmlquery v1.3.1 // indirect
	github.com/antchfx/xpath v1.1.10 // indirect
	github.com/bgentry/speakeasy v0.1.0 // indirect
	github.com/cenkalti/backoff/v4 v4.0.0 // indirect
	github.com/go-redis/redis/v7 v7.0.0-beta.3.0.20190824101152-d19aba07b476 // indirect
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/maruel/natural v1.1.1 // indirect
	github.com/onsi/ginkgo v1.10.3 // indirect
	github.com/onsi/gomega v1.7.1 // indirect
	github.com/openconfig/goyang v0.0.0-20200309174518-a00bece872fc // indirect
	github.com/philopon/go-toposort v0.0.0-20170620085441-9be86dbd762f // indirect
	github.com/stretchr/testify v1.9.0 // indirect
	go4.org/intern v0.0.0-20211027215823-ae77deb06f29 // indirect
	go4.org/unsafe/assume-no-moving-gc v0.0.0-20230525183740-e7c30c78aeb2 // indirect
	golang.org/x/sys v0.7.0 // indirect
	golang.org/x/text v0.9.0 // indirect
	google.golang.org/genproto v0.0.0-20230410155749-daa745c078e1 // indirect
	inet.af/netaddr v0.0.0-20230525184311-b8eac61e914a // indirect
)

replace github.com/Azure/sonic-mgmt-common => ../sonic-mgmt-common

// Glog patch needs to be updated to remove this.
replace github.com/golang/glog => github.com/golang/glog v0.0.0-20160126235308-23def4e6c14b
