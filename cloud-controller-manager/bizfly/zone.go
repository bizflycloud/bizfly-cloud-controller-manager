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

	"github.com/bizflycloud/gobizfly"
	"k8s.io/apimachinery/pkg/types"
	cloudprovider "k8s.io/cloud-provider"
)

type zones struct {
	region  string
	gclient *gobizfly.Client
}

func newZones(client *gobizfly.Client, region string) cloudprovider.Zones {
	return &zones{
		region:  region,
		gclient: client,
	}
}

// Kuberenetes uses this method to get the region that the program is running in.
func (z *zones) GetZone(ctx context.Context) (cloudprovider.Zone, error) {
	return cloudprovider.Zone{Region: z.region}, nil
}

// GetZoneByProviderID returns a cloudprovider.Zone from the droplet identified
// by providerID. GetZoneByProviderID only sets the Region field of the
// returned cloudprovider.Zone.
func (z *zones) GetZoneByProviderID(ctx context.Context, providerID string) (cloudprovider.Zone, error) {
	id, err := serverIDFromProviderID(providerID)
	if err != nil {
		everywhere_node, err := z.gclient.KubernetesEngine.GetEverywhere(ctx, id)
		if err != nil {
			return cloudprovider.Zone{}, err
		}
		return cloudprovider.Zone{
			FailureDomain: everywhere_node.Region,
			Region:        everywhere_node.Region}, nil
	}

	s, err := z.gclient.CloudServer.Get(ctx, id)
	if err != nil {
		return cloudprovider.Zone{}, err
	}

	return cloudprovider.Zone{
		Region:        z.region,
		FailureDomain: s.AvailabilityZone}, nil
}

// GetZoneByNodeName returns a cloudprovider.Zone from the droplet identified
// by nodeName. GetZoneByNodeName only sets the Region field of the returned
// cloudprovider.Zone.
func (z *zones) GetZoneByNodeName(ctx context.Context, nodeName types.NodeName) (cloudprovider.Zone, error) {
	s, err := serverByName(ctx, z.gclient, nodeName)
	if err != nil {
		// return cloudprovider.Zone{}, err
		return cloudprovider.Zone{
			FailureDomain: "HN",
			Region:        "HN"}, nil
	}

	return cloudprovider.Zone{
		Region:        z.region,
		FailureDomain: s.AvailabilityZone}, nil
}
