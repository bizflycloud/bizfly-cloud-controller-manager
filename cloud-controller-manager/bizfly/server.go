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
	"regexp"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/cloud-provider-openstack/pkg/util/metadata"
	"k8s.io/klog"

	"github.com/bizflycloud/gobizfly"
	"github.com/mitchellh/mapstructure"
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
	addrs, err := nodeAdddresses(server, nil)
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
		klog.V(3).Infof("Failed to extract server ID from providerID %s: %v", providerID, err)
		return []v1.NodeAddress{}, err
	}
	
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if errors.Is(err, gobizfly.ErrNotFound) {
		klog.V(3).Infof("Server with ID %s not found", serverID)
		return []v1.NodeAddress{}, fmt.Errorf("server %s not found: %w", serverID, err)
	}
	if err != nil {
		klog.V(2).Infof("Error getting server by ID %s: %v", serverID, err)
		return []v1.NodeAddress{}, err
	}
	
	var addrs []v1.NodeAddress
	if server != nil {
		addrs, err = nodeAdddresses(server, nil)
		if err != nil {
			klog.V(2).Infof("Error getting node addresses for server %s: %v", serverID, err)
			return nil, err
		}
	} else if node != nil {
		addrs, err = nodeAdddresses(nil, node)
		if err != nil {
			klog.V(2).Infof("Error getting node addresses for everywhere node %s: %v", serverID, err)
			return nil, err
		}
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
		klog.V(3).Infof("Failed to extract server ID from providerID %s: %v", providerID, err)
		return "", err
	}
	
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if errors.Is(err, gobizfly.ErrNotFound) {
		klog.V(3).Infof("Server with ID %s not found", serverID)
		return "", fmt.Errorf("server %s not found: %w", serverID, err)
	}
	if err != nil {
		klog.V(2).Infof("Error getting server by ID %s: %v", serverID, err)
		return "", err
	}
	
	if server != nil {
		var f *flavor
		err = mapstructure.Decode(server.Flavor, &f)
		if err != nil {
			klog.V(2).Infof("Error decoding flavor for server %s: %v", serverID, err)
			return "", err
		}
		return f.Name, nil
	}
	
	if node != nil {
		return node.UUID, nil
	}
	
	klog.V(3).Infof("No server or node found with ID %s", serverID)
	return "", fmt.Errorf("server %s not found", serverID)
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
		klog.V(3).Infof("Failed to extract server ID from providerID %s: %v", providerID, err)
		return false, nil // Return false without error to avoid breaking node operations
	}
	
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if errors.Is(err, gobizfly.ErrNotFound) {
		klog.V(3).Infof("Server with ID %s not found", serverID)
		return false, nil
	}
	if err != nil {
		klog.V(2).Infof("Error getting server by ID %s: %v", serverID, err)
		return false, err
	}
	
	return (server != nil || node != nil), nil
}

// InstanceShutdownByProviderID returns true if the instance is shutdown in cloudprovider
func (s *servers) InstanceShutdownByProviderID(ctx context.Context, providerID string) (bool, error) {
	klog.V(4).Infof("InstanceShutdownByProviderID(%v) is called", providerID)
	serverID, err := serverIDFromProviderID(providerID)
	if err != nil {
		klog.V(3).Infof("Failed to extract server ID from providerID %s: %v", providerID, err)
		return false, nil // Return false without error to avoid breaking node operations
	}
	
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if errors.Is(err, gobizfly.ErrNotFound) {
		klog.V(3).Infof("Server with ID %s not found", serverID)
		return false, nil
	}
	if err != nil {
		klog.V(2).Infof("Error getting server by ID %s: %v", serverID, err)
		return false, nil // Handle errors gracefully
	}
	
	if server != nil {
		if server.Status == instanceShutoff {
			return true, nil
		}
	} else if node != nil {
		if node.Deleted == true {
			return true, nil
		}
	}
	return false, nil
}

// nodeAddresses returns addresses of server
func nodeAdddresses(server *gobizfly.Server, node *gobizfly.EverywhereNode) ([]v1.NodeAddress, error) {
	var addresses []v1.NodeAddress
	if server != nil {
		for i := range server.IPAddresses.LanAddresses {
			addresses = append(
				addresses,
				v1.NodeAddress{Type: v1.NodeInternalIP, Address: server.IPAddresses.LanAddresses[i].Address},
			)
		}
		for i := range server.IPAddresses.WanV4Addresses {
			addresses = append(
				addresses,
				v1.NodeAddress{Type: v1.NodeExternalIP, Address: server.IPAddresses.WanV4Addresses[i].Address},
			)
		}
		return addresses, nil
	}
	if node != nil {
		addresses = append(addresses, v1.NodeAddress{Type: v1.NodeInternalIP, Address: node.PrivateIP})
		addresses = append(addresses, v1.NodeAddress{Type: v1.NodeExternalIP, Address: node.PublicIP})
		return addresses, nil
	}
	return addresses, nil
}

func serverByID(
	ctx context.Context,
	client *gobizfly.Client,
	id string,
) (*gobizfly.Server, *gobizfly.EverywhereNode, error) {
	server, err := client.CloudServer.Get(ctx, id)
	if err != nil {
		serverError := err
		if errors.Is(err, gobizfly.ErrNotFound) {
			node, err := client.KubernetesEngine.GetEverywhere(ctx, id)
			if err != nil {
				klog.V(5).Infof("error fetching node: %v, and cloud server: %v", serverError, err)
				return nil, nil, err
			}

			return nil, node, nil
		}

		return nil, nil, serverError
	}

	return server, nil, nil
}

func serverByName(ctx context.Context, client *gobizfly.Client, name types.NodeName) (*gobizfly.Server, error) {
	klog.V(5).Infof("Looking for server name: %s", string(name))
	
	maxAttempts := 5 // Increased from 3 to 5 for better reliability
	
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		servers, err := client.CloudServer.List(ctx, &gobizfly.ServerListOptions{})
		if err != nil {
			klog.V(2).Infof("Error when getting server list, attempt %d: %v", attempt, err)
			if attempt < maxAttempts {
				sleepDuration := backoff(attempt)
				klog.V(3).Infof("Retrying after %v", sleepDuration)
				time.Sleep(sleepDuration)
				continue
			}
			return nil, fmt.Errorf("failed to list servers after %d attempts: %w", attempt, err)
		}
		
		// Check if the server list is empty
		if len(servers) == 0 {
			klog.V(2).Infof("Retrieved empty server list, attempt %d", attempt)
			if attempt < maxAttempts {
				sleepDuration := backoff(attempt)
				klog.V(3).Infof("Retrying after %v", sleepDuration)
				time.Sleep(sleepDuration)
				continue
			}
			return nil, fmt.Errorf("server list is empty after %d attempts", maxAttempts)
		}

		for _, server := range servers {
			if strings.EqualFold(server.Name, string(name)) {
				klog.V(5).Infof("Found server %s with ID %s", server.Name, server.ID)
				return server, nil
			}
		}

		klog.V(2).Infof("Server %v not found in list of %d servers, attempt %d", name, len(servers), attempt)

		if attempt < maxAttempts {
			sleepDuration := backoff(attempt)
			klog.V(3).Infof("Retrying after %v", sleepDuration)
			time.Sleep(sleepDuration)
		}
	}

	return nil, cloudprovider.InstanceNotFound
}

func (s *servers) GetZoneByNodeName(
	ctx context.Context,
	client *gobizfly.Client,
	nodeName types.NodeName,
) (cloudprovider.Zone, error) {
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
	if providerID == "" {
		return "", fmt.Errorf("providerID cannot be empty")
	}

	if !strings.Contains(providerID, "://") {
		providerID = ProviderName + "://" + providerID
	}

	matches := providerIDRegexp.FindStringSubmatch(providerID)
	if len(matches) != 2 {
		return "", fmt.Errorf(
			"ProviderID \"%s\" didn't match expected format \"bizflycloud://InstanceID\"",
			providerID,
		)
	}
	
	// Check if we got an empty instance ID
	if matches[1] == "" {
		return "", fmt.Errorf("empty instance ID in providerID: %s", providerID)
	}
	
	return matches[1], nil
}

func backoff(attempt int) time.Duration {
	return time.Duration(attempt*(attempt+1)) * time.Second
}
