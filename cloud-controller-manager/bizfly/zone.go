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
		return cloudprovider.Zone{}, err
	}

	s, err := z.gclient.Server.Get(ctx, id)
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
		return cloudprovider.Zone{}, err
	}

	return cloudprovider.Zone{
		Region:        z.region,
		FailureDomain: s.AvailabilityZone}, nil
}
