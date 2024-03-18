package framework

import (
	"fmt"
	"github.com/bizflycloud/gobizfly"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"time"
)

var (
	Image    = "bizflycloud/bizfly-cloud-controller-manager:latest"
	ApiToken = ""
	Timeout  time.Duration

	KubeConfigFile         = ""
	TestServerResourceName = "e2e-test-server-" + characters(5)
)

const (
	TestServerImage = "nginx"
)

type Framework struct {
	restConfig *rest.Config
	kubeClient kubernetes.Interface
	namespace  string
	name       string

	bizflyClient gobizfly.Client
}

type rootInvocation struct {
	*Framework
	app string
}

type lbInvocation struct {
	*rootInvocation
}

type Invocation struct {
	*rootInvocation
	LoadBalancer *lbInvocation
}

func New(
	restConfig *rest.Config,
	kubeClient kubernetes.Interface,
	bizflyClient gobizfly.Client,
) *Framework {
	return &Framework{
		restConfig:   restConfig,
		kubeClient:   kubeClient,
		bizflyClient: bizflyClient,

		name:      "cloud-controller-manager",
		namespace: "ccm-" + characters(5),
	}
}

func (f *Framework) Invoke() *Invocation {
	r := &rootInvocation{
		Framework: f,
		app:       "csi-driver-e2e",
	}
	return &Invocation{
		rootInvocation: r,
		LoadBalancer:   &lbInvocation{rootInvocation: r},
	}
}

func (f *Framework) Recycle() error {
	if err := f.DeleteNamespace(); err != nil {
		return fmt.Errorf("failed to delete namespace (%s)", f.namespace)
	}

	f.namespace = "ccm-" + characters(5)
	if err := f.CreateNamespace(); err != nil {
		return fmt.Errorf("failed to create namespace (%s)", f.namespace)
	}
	return nil
}
