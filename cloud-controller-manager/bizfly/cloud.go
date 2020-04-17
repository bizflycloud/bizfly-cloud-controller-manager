package bizfly

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/bizflycloud/gobizfly"

	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog"
)

const (
	// ProviderName specifies the name for the Bizfly provider
	ProviderName string = "bizflycloud"

	bizflyCloudEmail    string = "BIZFLYCLOUD_EMAIL"
	bizflyCloudPassword string = "BIZFLYCLOUD_PASSWORD"
)

var (
	ctx = context.TODO()
)

type cloud struct {
	client    *gobizfly.Client
	instances cloudprovider.Instances
	// zones         cloudprovider.Zones
	loadbalancers cloudprovider.LoadBalancer
}

func newCloud() (cloudprovider.Interface, error) {
	username := os.Getenv(bizflyCloudEmail)
	password := os.Getenv(bizflyCloudPassword)

	bizflyClient, err := gobizfly.NewClient(gobizfly.WithTenantName(username))
	if err != nil {
		return nil, fmt.Errorf("Cannot create BizFly Cloud Client: %s", err)
	}

	token, err := bizflyClient.Token.Create(
		ctx,
		&gobizfly.TokenCreateRequest{
			Username: username,
			Password: password})

	if err != nil {
		return nil, fmt.Errorf("Cannot create token: %s", err)
	}

	bizflyClient.SetKeystoneToken(token.KeystoneToken)

	return &cloud{
		client:        bizflyClient,
		instances:     newInstances(bizflyClient),
		loadbalancers: newLoadBalancers(bizflyClient),
	}, nil
}

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(io.Reader) (cloudprovider.Interface, error) {
		return newCloud()
	})
}

func (c *cloud) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
}

func (c *cloud) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return c.loadbalancers, true
}

func (c *cloud) Instances() (cloudprovider.Instances, bool) {
	klog.V(1).Info("bizfly.Instances() called")
	klog.V(4).Info("Claiming to support Instances")
	return c.instances, true
}

func (c *cloud) Zones() (cloudprovider.Zones, bool) {
	klog.V(1).Info("Claiming to support Zones")
	return nil, false
}

func (c *cloud) Clusters() (cloudprovider.Clusters, bool) {
	return nil, false
}

func (c *cloud) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

func (c *cloud) ProviderName() string {
	return ProviderName
}

func (c *cloud) ScrubDNS(nameservers, searches []string) (nsOut, srchOut []string) {
	return nil, nil
}

func (c *cloud) HasClusterID() bool {
	return false
}
