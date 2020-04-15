module git.paas.vn/BizFly-PaaS-Cloud/bizfly-cloud-controller-manager

go 1.14

require (
	github.com/bizflycloud/gobizfly v0.0.0-20200415134205-d25de776bfc1
	github.com/blang/semver v3.5.1+incompatible // indirect
	github.com/checkpoint-restore/go-criu v0.0.0-20190109184317-bdb7599cd87b // indirect
	github.com/cloudflare/cfssl v0.0.0-20180726162950-56268a613adf // indirect
	github.com/coreos/etcd v3.3.20+incompatible // indirect
	github.com/coreos/go-systemd v0.0.0-20191104093116-d3cd4ed1dbcf // indirect
	github.com/coreos/rkt v1.30.0 // indirect
	github.com/emicklei/go-restful v2.12.0+incompatible // indirect
	github.com/go-openapi/spec v0.19.7 // indirect
	github.com/go-openapi/swag v0.19.9 // indirect
	github.com/godbus/dbus v4.1.0+incompatible // indirect
	github.com/golang/groupcache v0.0.0-20200121045136-8c9f03a8e57e // indirect
	github.com/golang/protobuf v1.4.0 // indirect
	github.com/google/certificate-transparency-go v1.0.21 // indirect
	github.com/hashicorp/golang-lru v0.5.4 // indirect
	github.com/heketi/rest v0.0.0-20180404230133-aa6a65207413 // indirect
	github.com/heketi/utils v0.0.0-20170317161834-435bc5bdfa64 // indirect
	github.com/imdario/mergo v0.3.9 // indirect
	github.com/mailru/easyjson v0.7.1 // indirect
	github.com/mitchellh/mapstructure v1.2.2
	github.com/opencontainers/runc v1.0.0-rc2.0.20190611121236-6cc515888830 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/prometheus/client_golang v1.5.1 // indirect
	github.com/prometheus/procfs v0.0.11 // indirect
	github.com/sirupsen/logrus v1.5.0 // indirect
	github.com/spf13/cobra v1.0.0 // indirect
	github.com/spf13/pflag v1.0.5
	github.com/xlab/handysort v0.0.0-20150421192137-fb3537ed64a1 // indirect
	go.uber.org/zap v1.14.1 // indirect
	golang.org/x/crypto v0.0.0-20200414173820-0848c9571904 // indirect
	golang.org/x/sys v0.0.0-20200413165638-669c56c373c4 // indirect
	google.golang.org/appengine v1.6.5 // indirect
	google.golang.org/genproto v0.0.0-20200413115906-b5235f65be36 // indirect
	google.golang.org/grpc v1.28.1 // indirect
	gopkg.in/square/go-jose.v2 v2.5.0 // indirect
	k8s.io/api v0.18.1
	k8s.io/apimachinery v0.18.1
	k8s.io/apiserver v0.18.1 // indirect
	k8s.io/client-go v1.5.1 // indirect
	k8s.io/cloud-provider v0.18.1
	k8s.io/cloud-provider-openstack v1.18.0
	k8s.io/component-base v0.18.1
	k8s.io/klog v1.0.0
	k8s.io/kube-controller-manager v0.18.1 // indirect
	k8s.io/kube-openapi v0.0.0-20200413232311-afe0b5e9f729 // indirect
	k8s.io/kubernetes v1.18.1
	k8s.io/utils v0.0.0-20200414100711-2df71ebbae66 // indirect
	sigs.k8s.io/structured-merge-diff v1.0.2 // indirect
	sigs.k8s.io/structured-merge-diff/v3 v3.0.0 // indirect
	vbom.ml/util v0.0.0-20160121211510-db5cfe13f5cc // indirect
)

replace k8s.io/api => k8s.io/api v0.0.0-20191016110408-35e52d86657a

replace k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.0.0-20191016113550-5357c4baaf65

replace k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20191004115801-a2eda9f80ab8

replace k8s.io/apiserver => k8s.io/apiserver v0.0.0-20191016112112-5190913f932d

replace k8s.io/cli-runtime => k8s.io/cli-runtime v0.0.0-20191016114015-74ad18325ed5

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20191016111102-bec269661e48

replace k8s.io/cloud-provider => k8s.io/cloud-provider v0.0.0-20191016115326-20453efc2458

replace k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.0.0-20191016115129-c07a134afb42

replace k8s.io/code-generator => k8s.io/code-generator v0.0.0-20191004115455-8e001e5d1894

replace k8s.io/component-base => k8s.io/component-base v0.0.0-20191016111319-039242c015a9

replace k8s.io/cri-api => k8s.io/cri-api v0.0.0-20190828162817-608eb1dad4ac

replace k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.0.0-20191016115521-756ffa5af0bd

replace k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.0.0-20191016112429-9587704a8ad4

replace k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.0.0-20191016114939-2b2b218dc1df

replace k8s.io/kube-proxy => k8s.io/kube-proxy v0.0.0-20191016114407-2e83b6f20229

replace k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.0.0-20191016114748-65049c67a58b

replace k8s.io/kubelet => k8s.io/kubelet v0.0.0-20191016114556-7841ed97f1b2

replace k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.0.0-20191016115753-cf0698c3a16b

replace k8s.io/metrics => k8s.io/metrics v0.0.0-20191016113814-3b1a734dba6e

replace k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.0.0-20191016112829-06bb3c9d77c9

replace k8s.io/kubectl => k8s.io/kubectl v0.0.0-20191016120415-2ed914427d51
