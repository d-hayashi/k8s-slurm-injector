module github.com/d-hayashi/k8s-slurm-injector

go 1.15

require (
	github.com/coreos/prometheus-operator v0.39.0
	github.com/google/goexpect v0.0.0-20200816234442-b5b77125c2c5
	github.com/google/goterm v0.0.0-20200907032337-555d40f16ae2 // indirect
	github.com/oklog/run v1.1.0
	github.com/prometheus/client_golang v1.8.0
	github.com/prometheus/common v0.14.0
	github.com/sirupsen/logrus v1.6.0
	github.com/slok/go-http-metrics v0.6.1
	github.com/slok/kubewebhook/v2 v2.0.0-beta.1
	github.com/stretchr/testify v1.6.1
	golang.org/x/crypto v0.0.0-20210322153248-0c34fe9e7dc2
	golang.org/x/net v0.0.0-20210316092652-d523dce5a7f4
	gopkg.in/alecthomas/kingpin.v2 v2.2.6
	k8s.io/api v0.19.6
	k8s.io/apimachinery v0.19.6
)

replace k8s.io/client-go => k8s.io/client-go v0.19.6
