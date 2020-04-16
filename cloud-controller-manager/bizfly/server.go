package bizfly

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog"

	"github.com/mitchellh/mapstructure"
	"k8s.io/cloud-provider-openstack/pkg/util/metadata"

	"github.com/bizflycloud/gobizfly"
)

const (
	instanceShutoff = "SHUTOFF"
)

type flavor struct {
	Name string `mapstructure:"name"`
}

type servers struct {
	gclient *gobizfly.Client
}

func newInstances(client *gobizfly.Client) cloudprovider.Instances {
	return &servers{gclient: client}
}

// NodeAddresses implements Instances.NodeAddresses
func (s *servers) NodeAddresses(ctx context.Context, nodeName types.NodeName) ([]v1.NodeAddress, error) {
	klog.V(4).Infof("NodeAddresses(%v) called", nodeName)
	server, err := serverByName(ctx, s.gclient, nodeName)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Server %v", server.Name)
	addrs, err := nodeAdddresses(server)
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("NodeAddresses(%v) => %v", nodeName, addrs)

	return addrs, nil
}

// NodeAddressesByProviderID returns the node addresses of an instances with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
func (s *servers) NodeAddressesByProviderID(ctx context.Context, providerID string) ([]v1.NodeAddress, error) {
	klog.V(4).Infof("NodeAddressesByProviderID(%v) called", providerID)
	serverID, err := serverIDFromProviderID(providerID)
	if err != nil {
		return []v1.NodeAddress{}, err
	}
	server, err := serverByID(ctx, s.gclient, serverID)
	if err != nil {
		return []v1.NodeAddress{}, err
	}
	addrs, err := nodeAdddresses(server)
	if err != nil {
		return nil, err
	}
	klog.V(4).Infof("NodeAddressesByProviderID(%v) => %v", providerID, addrs)
	return addrs, nil
}

// InstanceID returns the cloud provider ID of the node with the specified NodeName.
func (s *servers) InstanceID(ctx context.Context, nodeName types.NodeName) (string, error) {
	klog.V(4).Infof("InstaneID(%v) is called", nodeName)
	server, err := serverByName(ctx, s.gclient, nodeName)
	if err != nil {
		return "", err
	}
	return server.ID, nil
}

// InstanceType returns the type of the specified instance.
func (s *servers) InstanceType(ctx context.Context, nodeName types.NodeName) (string, error) {
	klog.V(4).Infof("InstanceType(%v) is called", nodeName)
	server, err := serverByName(ctx, s.gclient, nodeName)
	if err != nil {
		return "", err
	}
	var f *flavor
	err = mapstructure.Decode(server.Flavor, &f)
	if err != nil {
		return "", err
	}
	return f.Name, nil
}

// InstanceTypeByProviderID returns the type of the specified instance.
func (s *servers) InstanceTypeByProviderID(ctx context.Context, providerID string) (string, error) {
	klog.V(4).Infof("InstanceTypeByProviderID(%v) is called", providerID)
	serverID, err := serverIDFromProviderID(providerID)
	if err != nil {
		return "", err
	}
	server, err := serverByID(ctx, s.gclient, serverID)
	if err != nil {
		return "", err
	}
	var f *flavor
	err = mapstructure.Decode(server.Flavor, &f)
	if err != nil {
		return "", err
	}
	return f.Name, nil
}

// AddSSHKeyToAllInstances is not implemented; it always returns an error.
func (s *servers) AddSSHKeyToAllInstances(_ context.Context, _ string, _ []byte) error {
	return errors.New("not implemented")
}

// CurrentNodeName returns the name of the node we are currently running on
func (s *servers) CurrentNodeName(ctx context.Context, hostname string) (types.NodeName, error) {
	klog.V(4).Infof("CurrentNodeName(%v) is called", hostname)
	md, err := metadata.Get("")
	if err != nil {
		return "", err
	}
	return types.NodeName(md.Name), nil
}

// InstanceExistsByProviderID returns true if the instance for the given provider exists.
// If false is returned with no error, the instance will be immediately deleted by the cloud controller manager.
// This method should still return true for instances that exist but are stopped/sleeping.
func (s *servers) InstanceExistsByProviderID(ctx context.Context, providerID string) (bool, error) {
	klog.V(4).Infof("InstanceExistsByProviderID(%v) is called", providerID)
	serverID, err := serverIDFromProviderID(providerID)
	if err != nil {
		return false, err
	}
	_, err = serverByID(ctx, s.gclient, serverID)
	if err != nil {
		return false, err
	}
	return true, nil
}

// InstanceShutdownByProviderID returns true if the instance is shutdown in cloudprovider
func (s *servers) InstanceShutdownByProviderID(ctx context.Context, providerID string) (bool, error) {
	klog.V(4).Infof("InstanceShutdownByProviderID(%v) is called", providerID)
	serverID, err := serverIDFromProviderID(providerID)
	if err != nil {
		return false, err
	}

	server, err := serverByID(ctx, s.gclient, serverID)
	if err != nil {
		return false, err
	}
	if server.Status == instanceShutoff {
		return true, nil
	}
	return false, nil
}

// nodeAddresses returns addresses of server
func nodeAdddresses(server *gobizfly.Server) ([]v1.NodeAddress, error) {
	type address struct {
		Address string `mapstructure:"addr"`
		Version int    `mapstructure:"version"`
	}
	var serverAddresses map[string][]address

	var addresses []v1.NodeAddress

	addresses = append(addresses, v1.NodeAddress{Type: v1.NodeHostName, Address: server.Name})

	if err := mapstructure.Decode(server.Addresses, &serverAddresses); err != nil {
		klog.V(1).Infof("Cannot decode server: %v", err)
		return nil, err
	}

	for net, addr := range serverAddresses {
		if strings.Contains(net, "EXT") {
			addresses = append(addresses, v1.NodeAddress{Type: v1.NodeExternalIP, Address: addr[0].Address})
		} else {
			addresses = append(addresses, v1.NodeAddress{Type: v1.NodeInternalIP, Address: addr[0].Address})
		}
	}
	return addresses, nil
}

func serverByID(ctx context.Context, client *gobizfly.Client, id string) (*gobizfly.Server, error) {
	server, err := client.Server.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return server, nil
}

func serverByName(ctx context.Context, client *gobizfly.Client, name types.NodeName) (*gobizfly.Server, error) {
	servers, err := client.Server.List(ctx, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, server := range servers {
		if server.Name == string(name) {
			return server, nil
		}
	}
	return nil, cloudprovider.InstanceNotFound
}

func (s *servers) GetZoneByNodeName(ctx context.Context, client *gobizfly.Client, nodeName types.NodeName) (cloudprovider.Zone, error) {
	server, err := serverByName(ctx, client, nodeName)
	if err != nil {
		return cloudprovider.Zone{}, err
	}
	// TODO add region for zone
	zone := cloudprovider.Zone{FailureDomain: server.AvailabilityZone}
	klog.V(4).Infof("The instance %s in zone %v", server.Name, zone)
	return zone, nil
}

// If Instances.InstanceID or cloudprovider.GetInstanceProviderID is changed, the regexp should be changed too.
var providerIDRegexp = regexp.MustCompile(`^` + ProviderName + `://([^/]+)$`)

// instanceIDFromProviderID splits a provider's id and return instanceID.
// A providerID is build out of '${ProviderName}:///${instance-id}'which contains ':///'.
// See cloudprovider.GetInstanceProviderID and Instances.InstanceID.
func serverIDFromProviderID(providerID string) (instanceID string, err error) {

	// https://github.com/kubernetes/kubernetes/issues/85731
	if providerID != "" && !strings.Contains(providerID, "://") {
		providerID = ProviderName + "://" + providerID
	}

	matches := providerIDRegexp.FindStringSubmatch(providerID)
	if len(matches) != 2 {
		return "", fmt.Errorf("ProviderID \"%s\" didn't match expected format \"bizflycloud:///InstanceID\"", providerID)
	}
	return matches[1], nil
}
