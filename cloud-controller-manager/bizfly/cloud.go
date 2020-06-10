// This file is part of bizfly-cloud-controller-manager
//
// Copyright (C) 2020  BizFly Cloud
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>

package bizfly

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/bizflycloud/gobizfly"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog"
)

const (
	// ProviderName specifies the name for the Bizfly provider
	ProviderName  string = "bizflycloud"
	defaultRegion string = "HaNoi"
	authPassword  string = "password"
	authAppCred   string = "application_credential"
	defaultApiUrl string = "https://manage.bizflycloud.vn"

	bizflyCloudAuthMethod      string = "BIZFLYCLOUD_AUTH_METHOD"
	bizflyCloudEmailEnvName    string = "BIZFLYCLOUD_EMAIL"
	bizflyCloudPasswordEnvName string = "BIZFLYCLOUD_PASSWORD"
	bizflyCloudRegionEnvName   string = "BIZFLYCLOUD_REGION"
	bizflyCloudAppCredID       string = "BIZFLYCLOUD_APP_CREDENTIAL_ID"
	bizflyCloudAppCredSecret   string = "BIZFLYCLOUD_APP_CREDENTIAL_SECRET"
	bizflyCloudApiUrl          string = "BIZFLYCLOUD_API_URL"
	bizflyCloudTenantID        string = "BIZFLYCLOUD_TENANT_ID"
)

var (
	ctx = context.TODO()
)

type cloud struct {
	client        *gobizfly.Client
	instances     cloudprovider.Instances
	zones         cloudprovider.Zones
	loadbalancers cloudprovider.LoadBalancer
}

func newCloud() (cloudprovider.Interface, error) {
	authMethod := os.Getenv(bizflyCloudAuthMethod)
	username := os.Getenv(bizflyCloudEmailEnvName)
	password := os.Getenv(bizflyCloudPasswordEnvName)
	region := os.Getenv(bizflyCloudRegionEnvName)
	appCredId := os.Getenv(bizflyCloudAppCredID)
	appCredSecret := os.Getenv(bizflyCloudAppCredSecret)
	apiUrl := os.Getenv(bizflyCloudApiUrl)
	tenantId := os.Getenv(bizflyCloudTenantID)

	switch authMethod {
	case authPassword:
		{
			if username == "" {
				return nil, errors.New("You have to provide username variable")
			}
			if password == "" {
				return nil, errors.New("You have to provide password variable")
			}
		}
	case authAppCred:
		{
			if appCredId == "" {
				return nil, errors.New("You have to provide application credential ID")
			}
			if appCredSecret == "" {
				return nil, errors.New("You have to provide application credential secret")
			}
		}
	}

	if region == "" {
		region = defaultRegion
	}

	if apiUrl == "" {
		apiUrl = defaultApiUrl
	}

	bizflyClient, err := gobizfly.NewClient(gobizfly.WithTenantName(username), gobizfly.WithAPIUrl(apiUrl), gobizfly.WithTenantID(tenantId))
	if err != nil {
		return nil, fmt.Errorf("Cannot create BizFly Cloud Client: %w", err)
	}

	token, err := bizflyClient.Token.Create(
		ctx,
		&gobizfly.TokenCreateRequest{
			AuthMethod:    authMethod,
			Username:      username,
			Password:      password,
			AppCredID:     appCredId,
			AppCredSecret: appCredSecret})

	if err != nil {
		return nil, fmt.Errorf("Cannot create token: %w", err)
	}

	bizflyClient.SetKeystoneToken(token.KeystoneToken)

	return &cloud{
		client:        bizflyClient,
		instances:     newInstances(bizflyClient),
		loadbalancers: newLoadBalancers(bizflyClient),
		zones:         newZones(bizflyClient, region),
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
	klog.V(4).Info("Claiming to support LoadBalancers")
	return c.loadbalancers, true
}

func (c *cloud) Instances() (cloudprovider.Instances, bool) {
	klog.V(1).Info("bizfly.Instances() called")
	klog.V(4).Info("Claiming to support Instances")
	return c.instances, true
}

func (c *cloud) Zones() (cloudprovider.Zones, bool) {
	klog.V(1).Info("Claiming to support Zones")
	return c.zones, true
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
