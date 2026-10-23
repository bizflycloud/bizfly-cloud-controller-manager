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
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/cloud-provider-openstack/pkg/util/metadata"
	"k8s.io/klog/v2"

	"github.com/bizflycloud/gobizfly"
	"github.com/mitchellh/mapstructure"
)

const (
	instanceShutoff = "SHUTOFF"

	serverByNameAttempts = 12 // 12 attempts * 15s = 3 minutes
)

// var so tests can shorten it
var serverByNameRetryInterval = 15 * time.Second

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
		return []v1.NodeAddress{}, err
	}
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if err != nil {
		return []v1.NodeAddress{}, err
	}
	var addrs []v1.NodeAddress
	if server != nil {
		addrs, err = nodeAdddresses(server, nil)
		if err != nil {
			return nil, err
		}
	} else if node != nil {
		addrs, err = nodeAdddresses(nil, node)
		if err != nil {
			return nil, err
		}
	}
	klog.V(4).Infof("NodeAddressesByProviderID(%v) => %v", providerID, addrs)
	return addrs, nil
}

// InstanceID returns the cloud provider ID of the node with the specified NodeName.
func (s *servers) InstanceID(ctx context.Context, nodeName types.NodeName) (string, error) {
	if nodeName == "" {
		klog.Errorf("Empty nodeName provided to InstanceID")
		return "", fmt.Errorf("nodeName cannot be empty")
	}
	klog.Infof("InstanceID(%v) is called", nodeName)
	server, err := serverByName(ctx, s.gclient, nodeName)
	if err != nil {
		return "", err
	}
	klog.Infof("InstanceID(%v) => %s", nodeName, server.ID)
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
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if err != nil {
		return "", err
	}
	if server != nil {
		var f *flavor
		err = mapstructure.Decode(server.Flavor, &f)
		if err != nil {
			return "", err
		}
		return f.Name, nil
	}
	if node != nil {
		return node.UUID, nil
	}
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
		return false, err
	}
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if errors.Is(err, gobizfly.ErrNotFound) {
		// false makes the cloud node lifecycle controller delete the Node object right away,
		// and the node loses every label set after it registered. Confirm before saying so.
		gone, confirmErr := confirmServerGone(ctx, s.gclient, serverID)
		if confirmErr != nil {
			klog.Warningf("InstanceExistsByProviderID - %s not found but could not confirm it: %v", serverID, confirmErr)
			return false, confirmErr
		}
		return !gone, nil
	}
	if err != nil {
		return false, err
	}
	if server != nil || node != nil {
		return true, nil
	} else {
		return false, nil
	}
}

// confirmServerGone double-checks a NotFound from CloudServer.Get against the project server list,
// so a transient 404 (token, API gateway) does not get a live node deleted.
func confirmServerGone(ctx context.Context, client *gobizfly.Client, serverID string) (bool, error) {
	servers, err := client.CloudServer.List(ctx, &gobizfly.ServerListOptions{})
	if err != nil {
		return false, err
	}
	if len(servers) == 0 {
		// The node itself still exists in the cluster, so an empty list is not trustworthy
		return false, fmt.Errorf("empty server list")
	}
	for _, server := range servers {
		if server.ID == serverID {
			klog.Warningf("confirmServerGone - %s returned NotFound but is still listed, keeping the node", serverID)
			return false, nil
		}
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
	server, node, err := serverByID(ctx, s.gclient, serverID)
	if errors.Is(err, gobizfly.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if server != nil {
		if server.Status == instanceShutoff {
			return true, nil
		}
	} else if node != nil {
		if node.Deleted {
			return true, nil
		}
	}
	return false, nil
}

type openstackAddress struct {
	IPType string `mapstructure:"OS-EXT-IPS:type"`
	Addr   string `mapstructure:"addr"`
}

func isPublicIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
}

// nodeAddresses returns addresses of server
func nodeAdddresses(server *gobizfly.Server, node *gobizfly.EverywhereNode) ([]v1.NodeAddress, error) {
	var addresses []v1.NodeAddress
	added := make(map[string]bool)

	addAddr := func(addrType v1.NodeAddressType, ip string) {
		if ip == "" || added[ip] {
			return
		}
		addresses = append(addresses, v1.NodeAddress{Type: addrType, Address: ip})
		added[ip] = true
	}

	if server != nil {
		// IPAddresses first, in the v2.0.4 order: nodeAddressForLB takes the first InternalIP,
		// so it has to stay the LAN address and must not change between syncs.
		for i := range server.IPAddresses.LanAddresses {
			addAddr(v1.NodeInternalIP, server.IPAddresses.LanAddresses[i].Address)
		}
		for i := range server.IPAddresses.WanV4Addresses {
			addAddr(v1.NodeExternalIP, server.IPAddresses.WanV4Addresses[i].Address)
		}
		for i := range server.IPAddresses.WanV6Addresses {
			addAddr(v1.NodeExternalIP, server.IPAddresses.WanV6Addresses[i].Address)
		}

		// Then whatever only the Nova Addresses map knows about (e.g. floating IPs),
		// network names sorted so the order is stable.
		if server.Addresses != nil {
			var rawAddresses map[string][]openstackAddress
			if err := mapstructure.Decode(server.Addresses, &rawAddresses); err == nil {
				netNames := make([]string, 0, len(rawAddresses))
				for netName := range rawAddresses {
					netNames = append(netNames, netName)
				}
				sort.Strings(netNames)
				for _, netName := range netNames {
					for _, addr := range rawAddresses[netName] {
						if addr.IPType == "floating" ||
							strings.HasPrefix(strings.ToUpper(netName), "EXT") ||
							strings.Contains(strings.ToLower(netName), "wan") ||
							isPublicIP(addr.Addr) {
							addAddr(v1.NodeExternalIP, addr.Addr)
						} else {
							addAddr(v1.NodeInternalIP, addr.Addr)
						}
					}
				}
			} else {
				klog.Warningf("nodeAdddresses - Failed to decode server.Addresses: %v", err)
			}
		}

		return addresses, nil
	}
	if node != nil {
		if node.PrivateIP != "" {
			addAddr(v1.NodeInternalIP, node.PrivateIP)
		}
		if node.PublicIP != "" {
			addAddr(v1.NodeExternalIP, node.PublicIP)
		}
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
		if errors.Is(err, gobizfly.ErrNotFound) {
			node, everywhereErr := client.KubernetesEngine.GetEverywhere(ctx, id)
			if everywhereErr == nil && node != nil {
				return nil, node, nil
			}
			klog.V(5).Infof("serverByID - Server %s not found in CloudServer (%v) and Everywhere (%v)", id, err, everywhereErr)
			return nil, nil, gobizfly.ErrNotFound
		}

		return nil, nil, err
	}

	return server, nil, nil
}

func serverByName(ctx context.Context, client *gobizfly.Client, name types.NodeName) (*gobizfly.Server, error) {
	klog.V(4).Infof("serverByName - Looking for server name: %s", string(name))

	for attempt := 1; attempt <= serverByNameAttempts; attempt++ {
		server, err := findServerByName(ctx, client, string(name))
		if err != nil {
			klog.Infof("serverByName - Error when getting server list, attempt %d: %v", attempt, err)
			if attempt == serverByNameAttempts {
				return nil, fmt.Errorf("failed to list servers after %d attempts: %w", attempt, err)
			}
		} else if server != nil {
			klog.V(4).Infof("serverByName - Found server ID: %s, fetching full details", server.ID)
			fullServer, err := client.CloudServer.Get(ctx, server.ID)
			if err != nil {
				klog.Warningf("serverByName - Failed to get full server details for %s: %v, falling back to list data", server.ID, err)
				return server, nil
			}
			return fullServer, nil
		} else {
			klog.Infof("serverByName - Server %v not found in list, attempt %d", name, attempt)
		}

		if attempt < serverByNameAttempts {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(serverByNameRetryInterval):
			}
		}
	}

	return nil, cloudprovider.InstanceNotFound
}

// findServerByName asks the API to filter by name first; node names are lowercase while server
// names may not be, so it falls back to the full list when the filtered one has no match.
func findServerByName(ctx context.Context, client *gobizfly.Client, name string) (*gobizfly.Server, error) {
	for _, opts := range []*gobizfly.ServerListOptions{{Name: name}, {}} {
		servers, err := client.CloudServer.List(ctx, opts)
		if err != nil {
			return nil, err
		}
		for _, server := range servers {
			if strings.EqualFold(server.Name, name) && server.ID != "" {
				return server, nil
			}
		}
	}
	return nil, nil
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
// Matches "bizflycloud://<id>", "bizflycloud:///<id>", and "bizflycloud://<region>/<id>".
var providerIDRegexp = regexp.MustCompile(`^` + ProviderName + `:///*(?:[^/]+/)?([^/]+)$`)

// instanceIDFromProviderID splits a provider's id and return instanceID.
// A providerID is build out of '${ProviderName}:///${instance-id}'which contains ':///'.
// See cloudprovider.GetInstanceProviderID and Instances.InstanceID.
func serverIDFromProviderID(providerID string) (instanceID string, err error) {
	// https://github.com/kubernetes/kubernetes/issues/85731
	klog.V(5).Infof("serverIDFromProviderID - providerID: %s", providerID)
	if providerID != "" && !strings.Contains(providerID, "://") {
		providerID = ProviderName + "://" + providerID
	}

	matches := providerIDRegexp.FindStringSubmatch(providerID)
	if len(matches) != 2 {
		return "", fmt.Errorf(
			"ProviderID \"%s\" didn't match expected format \"bizflycloud:///InstanceID\"",
			providerID,
		)
	}
	return matches[1], nil
}
