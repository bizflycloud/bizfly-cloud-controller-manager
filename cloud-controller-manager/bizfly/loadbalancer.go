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
	"strconv"
	"strings"
	"time"

	"github.com/bizflycloud/gobizfly"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	cloudprovider "k8s.io/cloud-provider"
	cpoerrors "k8s.io/cloud-provider-openstack/pkg/util/errors"
	"k8s.io/klog"
)

const (
	// loadbalancerActive* is configuration of exponential backoff for
	// going into ACTIVE loadbalancer provisioning status. Starting with 1
	// seconds, multiplying by 1.2 with each step and taking 25 steps at maximum
	// it will time out after 392s, which roughly corresponds to 390s
	loadbalancerActiveInitDelay = 1 * time.Second
	loadbalancerActiveFactor    = 1.2
	loadbalancerActiveSteps     = 25

	activeStatus   = "ACTIVE"
	errorStatus    = "ERROR"
	PROXY_PROTOCOL = "PROXY"
	ROUND_ROBIN    = "ROUND_ROBIN"

	INTERNAL_NETWORK_TYPE = "internal"
	EXTERNAL_NETWORK_TYPE = "external"

	SMALL_LB_TYPE  = "small"
	MEDIUM_LB_TYPE = "medium"
	// loadbalancerDelete* is configuration of exponential backoff for
	// waiting for delete operation to complete. Starting with 1
	// seconds, multiplying by 1.2 with each step and taking 13 steps at maximum
	// it will time out after 32s, which roughly corresponds to 30s
	loadbalancerDeleteInitDelay = 1 * time.Second
	loadbalancerDeleteFactor    = 1.2
	loadbalancerDeleteSteps     = 13

	annotationLoadBalancerNetworkType = "kubernetes.bizflycloud.vn/load-balancer-network-type"

	annotationLoadBalancerType    = "kubernetes.bizflycloud.vn/load-balancer-type"
	annotationEnableProxyProtocol = "kubernetes.bizflycloud.vn/enable-proxy-protocol"
	annotationVPCNetworkName      = "kubernetes.bizflycloud.vn/vpc-network-name"

	annotationEnableIngressHostname        = "kubernetes.bizflycloud.vn/enable-ingress-hostname"
	annotationLoadBalancerTargetNodeLabels = "kubernetes.bizflycloud.vn/target-node-labels"
	// See https://nip.io
	defaultProxyHostnameSuffix = "nip.io"
)

// ErrNotFound represents error if the resource not found.
var ErrNotFound = errors.New("failed to find object")

// ErrMultipleResults represents error if get multiple results where only one expected.
var ErrMultipleResults = errors.New("multiple results where only one expected")

// ErrNoAddressFound is used when we cannot find an ip address for the host
var ErrNoAddressFound = errors.New("no address found for host")

type loadbalancers struct {
	gclient *gobizfly.Client
}

func newLoadBalancers(client *gobizfly.Client) cloudprovider.LoadBalancer {
	return &loadbalancers{gclient: client}
}

// GetLoadBalancer returns whether the specified load balancer exists, and
// Parameter 'clusterName' is the name of the cluster as presented to kube-controller-manager
func (l *loadbalancers) GetLoadBalancer(ctx context.Context, clusterName string, service *v1.Service) (*v1.LoadBalancerStatus, bool, error) {
	klog.Infof("GetLoadBalancer(%s)", clusterName)
	name := l.GetLoadBalancerName(ctx, clusterName, service)
	loadbalancer, err := getLBByName(ctx, l.gclient, name)

	if err != nil {
		if err == ErrNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	status := &v1.LoadBalancerStatus{}
	status.Ingress = []v1.LoadBalancerIngress{{IP: loadbalancer.VipAddress}}

	return status, true, nil
}

// cutString makes sure the string length doesn't exceed 255, which is usually the maximum string length in OpenStack.
func cutString(original string) string {
	if len(original) > 255 {
		original = original[:255]
	}
	return original
}

// getBoolFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's boolean value or a specified defaultSetting
func getBoolFromServiceAnnotation(service *v1.Service, annotationKey string, defaultSetting bool) (bool, error) {
	klog.Infof("getBoolFromServiceAnnotation(%s/%s, %v, %v)", service.Namespace, service.Name, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		returnValue := false
		switch annotationValue {
		case "true":
			returnValue = true
		case "false":
			returnValue = false
		default:
			return returnValue, fmt.Errorf("unknown %s annotation: %v, specify \"true\" or \"false\" ", annotationKey, annotationValue)
		}

		klog.Infof("Found a Service Annotation: %v = %v", annotationKey, returnValue)
		return returnValue, nil
	}
	klog.Infof("Could not find a Service Annotation; falling back to default setting: %v = %v", annotationKey, defaultSetting)
	return defaultSetting, nil
}

// GetLoadBalancerName returns the constructed load balancer name.
func (l *loadbalancers) GetLoadBalancerName(ctx context.Context, clusterName string, service *v1.Service) string {
	name := fmt.Sprintf("kube_service_%s_%s_%s", clusterName, service.Namespace, service.Name)
	return cutString(name)
}

// EnsureLoadBalancer creates a new load balancer 'name', or updates the existing one. Returns the status of the balancer
// Implementations must treat the *v1.Service and *v1.Node
// parameters as read-only and not modify them.
// Parameter 'clusterName' is the name of the cluster as presented to kube-controller-manager
func (l *loadbalancers) EnsureLoadBalancer(ctx context.Context, clusterName string, apiService *v1.Service, nodes []*v1.Node) (*v1.LoadBalancerStatus, error) {

	serviceName := fmt.Sprintf("%s/%s", apiService.Namespace, apiService.Name)
	klog.Infof("EnsureLoadBalancer(%s, %s)", clusterName, serviceName)

	if len(nodes) == 0 {
		return nil, fmt.Errorf("there are no available nodes for LoadBalancer service %s", serviceName)
	}

	ports := apiService.Spec.Ports
	if len(ports) == 0 {
		return nil, fmt.Errorf("no ports provided to openstack load balancer")
	}

	// Network type of load balancer: internal or external
	networkType := getStringFromServiceAnnotation(apiService, annotationLoadBalancerNetworkType, EXTERNAL_NETWORK_TYPE)
	lbType := getStringFromServiceAnnotation(apiService, annotationLoadBalancerType, MEDIUM_LB_TYPE)
	useProxyProtocol, err := getBoolFromServiceAnnotation(apiService, annotationEnableProxyProtocol, false)
	enableIngressHostname, err := getBoolFromServiceAnnotation(apiService, annotationEnableIngressHostname, false)
	vpcNetworkName := getStringFromServiceAnnotation(apiService, annotationVPCNetworkName, "")
	if err != nil {
		return nil, err
	}
	// Affinity Configuration for pool
	affinity := apiService.Spec.SessionAffinity
	var persistence *gobizfly.SessionPersistence
	switch affinity {
	case v1.ServiceAffinityNone:
		persistence = nil
	case v1.ServiceAffinityClientIP:
		persistence = &gobizfly.SessionPersistence{Type: "SOURCE_IP"}
	default:
		return nil, fmt.Errorf("unsupported load balancer affinity: %s", affinity)
	}

	// Check load balancer is exist or not
	name := l.GetLoadBalancerName(ctx, clusterName, apiService)
	loadbalancer, err := getLBByName(ctx, l.gclient, name)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			klog.Errorf("error getting loadbalancer for Service %s: %s", serviceName, err)
			return nil, fmt.Errorf("error getting loadbalancer for Service %s: %s", serviceName, err)
		}
		// Create new load balancer is the load balancer is not exist.
		klog.V(2).Infof("Creating loadbalancer %s", name)

		loadbalancer, err = l.createLoadBalancer(ctx, name, networkType, lbType, vpcNetworkName)
		if err != nil {
			klog.Errorf("error creating loadbalancer %s: %s", name, err)
			return nil, fmt.Errorf("error creating loadbalancer %s: %s", name, err)
		}

	} else {
		klog.V(2).Infof("LoadBalancer %s already exists", loadbalancer.Name)
	}
	if loadbalancer != nil {
		if loadbalancer.ProvisioningStatus == "ACTIVE" {
			oldListeners, err := getListenersByLoadBalancerID(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				klog.Errorf("error getting LB %s listeners: %s", loadbalancer.Name, err)
				return nil, fmt.Errorf("error getting LB %s listeners: %s", loadbalancer.Name, err)
			}
			// get pools
			for portIndex, port := range ports {
				listener, _ := getListenerForPort(oldListeners, port)
				if listener == nil {
					continue
				}
				oldPool, _ := getPoolByListenerID(ctx, l.gclient, loadbalancer.ID, listener.ID)
				// get current pool protocol
				if oldPool.Protocol == "PROXY" && useProxyProtocol {
					klog.Infof("Pool %s already used PROXY protocol", oldPool.ID)
					continue
				} else if !useProxyProtocol && oldPool.Protocol != "PROXY" {
					klog.Infof("Pool %s already used %s protocol", oldPool.ID, oldPool.Protocol)
					continue
				} else {
					klog.Infof("Changing pool protocol detected")
					klog.Infof("Current pool protocol: %v", oldPool.Protocol)
					klog.Infof("Use Proxy Protocol: %v", useProxyProtocol)

					err := l.changePoolProtocol(ctx, name, useProxyProtocol, listener, portIndex, port, loadbalancer.ID, persistence, nodes, oldPool)
					if err != nil {
						return nil, err
					}
				}
			}
		}
	}

	provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
	if err != nil {
		klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE, current provisioning status %s", provisioningStatus)
		return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE, current provisioning status %s", provisioningStatus)
	}

	oldListeners, err := getListenersByLoadBalancerID(ctx, l.gclient, loadbalancer.ID)
	if err != nil {
		klog.Errorf("error getting LB %s listeners: %s", loadbalancer.Name, err)
		return nil, fmt.Errorf("error getting LB %s listeners: %s", loadbalancer.Name, err)
	}

	for portIndex, port := range ports {
		listener, err := getListenerForPort(oldListeners, port)

		if err != nil {
			listener, err = l.createListener(ctx, portIndex, int(port.Port), string(port.Protocol), name, loadbalancer.ID)
			if err != nil {
				return nil, err
			}
			provisioningStatus, err = waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE, current provisioning status %s", provisioningStatus)
				return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE, current provisioning status %s", provisioningStatus)
			}
		}
		// After all ports have been processed, remaining listeners are removed as obsolete.
		// Pop valid listeners.
		if len(oldListeners) > 0 {
			oldListeners = popListener(oldListeners, listener.ID)
		}
		pool, err := getPoolByListenerID(ctx, l.gclient, loadbalancer.ID, listener.ID)

		if err != nil && !errors.Is(err, ErrNotFound) {
			klog.Errorf("error getting pool for listener %s: %s", listener.ID, err)
			return nil, fmt.Errorf("error getting pool for listener %s: %s", listener.ID, err)
		}

		if pool == nil {
			// Create a new pool
			// use protocol of listener
			pool, err = l.createPoolForListener(ctx, listener, portIndex, loadbalancer.ID, name, persistence, useProxyProtocol, true)
			klog.Infof("Pool created for listener %s: %s", listener.ID, pool.ID)
			if err != nil {
				return nil, err
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating pool, current provisioning status %s", provisioningStatus)
				return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating pool, current provisioning status %s", provisioningStatus)
			}
		}

		nodesList := filterTargetNodes(apiService, nodes)
		when := "ensuring loadbalancer"
		updatedMembers, err := batchUpdateMembers(ctx, l.gclient, pool.ID, loadbalancer.ID, nodesList, &port, portIndex, name, when)
		if err != nil {
			return nil, err
		}
		klog.Infof("number of members after batchUpdate for port %v: %v", port.Port, updatedMembers)

		monitorID := pool.HealthMonitorID
		if monitorID == "" {
			klog.Infof("Creating monitor for pool %s", pool.ID)
			//monitorProtocol := string(port.Protocol)
			//if port.Protocol == v1.ProtocolUDP {
			//	monitorProtocol = "UDP-CONNECT"
			//}
			//TODO use http monitor
			monitor, err := l.gclient.HealthMonitor.Create(ctx, pool.ID, &gobizfly.HealthMonitorCreateRequest{
				Name:           cutString(fmt.Sprintf("monitor_%d_%s)", portIndex, name)),
				Type:           "TCP",
				Delay:          3,
				TimeOut:        3,
				MaxRetries:     3,
				MaxRetriesDown: 3,
			})
			if err != nil {
				return nil, fmt.Errorf("error creating LB pool healthmonitor: %v", err)
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating monitor, current provisioning status %s", provisioningStatus)
			}
			monitorID = monitor.ID
		}
	}
	// All remaining listeners are obsolete, delete
	for _, listener := range oldListeners {
		klog.Infof("Deleting obsolete listener %s:", listener.ID)
		// get pool for listener
		pool, err := getPoolByListenerID(ctx, l.gclient, loadbalancer.ID, listener.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, fmt.Errorf("error getting pool for obsolete listener %s: %v", listener.ID, err)
		}
		if pool != nil {
			// get and delete monitor
			monitorID := pool.HealthMonitorID
			if monitorID != "" {
				klog.Infof("Deleting health monitor %s for pool %s", monitorID, pool.ID)
				err := l.gclient.HealthMonitor.Delete(ctx, monitorID)
				if err != nil {
					return nil, fmt.Errorf("Error deleteing LB Pool healthmonitor %v", err)
				}
			}
			// get and delete pool members
			nodesList := []*v1.Node{}
			port := v1.ServicePort{}
			portIndex := 0
			when := "delete remaining obsolete listener"
			_, err := batchUpdateMembers(ctx, l.gclient, pool.ID, loadbalancer.ID, nodesList, &port, portIndex, name, when)
			if err != nil {
				return nil, err
			}
			klog.Infof("Deleting obsolete pool %s for listener %s", pool.ID, listener.ID)
			// delete pool
			err = l.gclient.Pool.Delete(ctx, pool.ID)
			if err != nil && !cpoerrors.IsNotFound(err) {
				klog.Errorf("error deleting obsolete pool %s for listener %s: %v", pool.ID, listener.ID, err)
				return nil, fmt.Errorf("error deleting obsolete pool %s for listener %s: %v", pool.ID, listener.ID, err)
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting pool, current provisioning status %s", provisioningStatus)
				return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting pool, current provisioning status %s", provisioningStatus)
			}
		}
		// delete listener
		err = l.gclient.Listener.Delete(ctx, listener.ID)
		if err != nil && !cpoerrors.IsNotFound(err) {
			return nil, fmt.Errorf("error deleteting obsolete listener: %v", err)
		}
		provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
		if err != nil {
			klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting listener, current provisioning status %s", provisioningStatus)
			return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting listener, current provisioning status %s", provisioningStatus)
		}
		klog.V(2).Infof("Deleted obsolete listener: %s", listener.ID)
	}

	status := &v1.LoadBalancerStatus{}
	if net.ParseIP(loadbalancer.VipAddress) != nil {
		status.Ingress = []v1.LoadBalancerIngress{{IP: loadbalancer.VipAddress}}
		// If the load balancer is using the PROXY protocol, expose its IP address via
		// the Hostname field to prevent kube-proxy from injecting an iptables bypass.
		// This is a workaround until
		// https://github.com/kubernetes/enhancements/tree/master/keps/sig-network/1860-kube-proxy-IP-node-binding
		// is implemented (maybe in v1.22).
		if useProxyProtocol && enableIngressHostname {
			fakeHostname := fmt.Sprintf("%s.%s", status.Ingress[0].IP, defaultProxyHostnameSuffix)
			status.Ingress = []v1.LoadBalancerIngress{{Hostname: fakeHostname}}
		}
	} else {
		status.Ingress = []v1.LoadBalancerIngress{{Hostname: loadbalancer.VipAddress}}
	}
	return status, nil
}

// UpdateLoadBalancer updates hosts under the specified load balancer.
// Implementations must treat the *v1.Service and *v1.Node
// parameters as read-only and not modify them.
// Parameter 'clusterName' is the name of the cluster as presented to kube-controller-manager
func (l *loadbalancers) UpdateLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) error {
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	klog.Infof("UpdateLoadBalancer(%v, %s, %v)", clusterName, serviceName, nodes)

	ports := service.Spec.Ports
	if len(ports) == 0 {
		return fmt.Errorf("no ports provided to bizflycloud load balancer")
	}

	name := l.GetLoadBalancerName(ctx, clusterName, service)
	lb, err := getLBByName(ctx, l.gclient, name)

	if err != nil {
		if errors.Is(err, ErrNotFound) {
			klog.Errorf("loadbalancer does not exist for Service %s", serviceName)
			return fmt.Errorf("loadbalancer does not exist for Service %s", serviceName)
		}
		return err
	}

	type portKey struct {
		Protocol string
		Port     int
	}
	var listenerIDs []string
	lbListeners := make(map[portKey]*gobizfly.Listener)
	listeners, err := getListenersByLoadBalancerID(ctx, l.gclient, lb.ID)
	if err != nil {
		klog.Errorf("error getting listeners for LB %s: %s", lb.ID, err)
		return fmt.Errorf("error getting listeners for LB %s: %s", lb.ID, err)
	}

	for _, l := range listeners {
		key := portKey{Protocol: string(l.Protocol), Port: int(l.ProtocolPort)}
		lbListeners[key] = l
		listenerIDs = append(listenerIDs, l.ID)
	}

	// Get all pools for this loadbalancer, by listener ID.
	lbPools := make(map[string]*gobizfly.Pool)
	for _, listenerID := range listenerIDs {
		pool, err := getPoolByListenerID(ctx, l.gclient, lb.ID, listenerID)
		if err != nil {
			klog.Errorf("error getting pool for listener %s: %s", listenerID, err)
			return fmt.Errorf("error getting pool for listener %s: %s", listenerID, err)
		}
		lbPools[listenerID] = pool
	}

	addrs := make(map[string]*v1.Node)
	nodesList := filterTargetNodes(service, nodes)
	for _, node := range nodesList {
		addr, err := nodeAddressForLB(node)
		if err != nil {
			return err
		}
		addrs[addr] = node
	}

	// Check for adding/removing members associated with each port
	for portIndex, port := range ports {
		// Get listener associated with this port
		listener, ok := lbListeners[portKey{
			Protocol: string(port.Protocol),
			Port:     int(port.Port),
		}]
		if !ok {
			klog.Errorf("loadbalancer %s does not contain required listener for port %d and protocol %s", lb.ID, port.Port, port.Protocol)
			return fmt.Errorf("loadbalancer %s does not contain required listener for port %d and protocol %s", lb.ID, port.Port, port.Protocol)
		}

		// Get pool associated with this listener
		pool, ok := lbPools[listener.ID]
		if !ok {
			return fmt.Errorf("loadbalancer %s does not contain required pool for listener %s", lb.ID, listener.ID)
		}

		updatedMembers, err := batchUpdateMembers(ctx, l.gclient, pool.ID, lb.ID, nodesList, &port, portIndex, name, "updating loadbalancer")
		if err != nil {
			return err
		}
		klog.Infof("number of members after batchUpdate for port %v: %v", port.Port, updatedMembers)
	}
	return nil
}

// EnsureLoadBalancerDeleted deletes the specified load balancer if it
// exists, returning nil if the load balancer specified either didn't exist or
// was successfully deleted.
// This construction is useful because many cloud providers' load balancers
// have multiple underlying components, meaning a Get could say that the LB
// doesn't exist even if some part of it is still laying around.
// Implementations must treat the *v1.Service parameter as read-only and not modify it.
// Parameter 'clusterName' is the name of the cluster as presented to kube-controller-manager
func (l *loadbalancers) EnsureLoadBalancerDeleted(ctx context.Context, clusterName string, service *v1.Service) error {
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	klog.Infof("EnsureLoadBalancerDeleted(%s, %s)", clusterName, serviceName)

	name := l.GetLoadBalancerName(ctx, clusterName, service)
	lb, err := getLBByName(ctx, l.gclient, name)
	if err != nil {
		return err
	}
	err = l.gclient.LoadBalancer.Delete(ctx, &gobizfly.LoadBalancerDeleteRequest{
		Cascade: true,
		ID:      lb.ID})
	if err != nil {
		return err
	}
	err = waitLoadbalancerDeleted(ctx, l.gclient, lb.ID)
	if err != nil {
		klog.Errorf("failed to delete loadbalancer: %v", err)
		return fmt.Errorf("failed to delete loadbalancer: %v", err)
	}
	return nil
}

func getLBByName(ctx context.Context, client *gobizfly.Client, name string) (*gobizfly.LoadBalancer, error) {
	loadbalancers, err := client.LoadBalancer.List(ctx, &gobizfly.ListOptions{})
	if err != nil {
		klog.Infof("Cannot get loadbalancers in your account: %v", err)
		return nil, err
	}
	for _, lb := range loadbalancers {
		if lb.Name == name {
			klog.Infof("Selected Load Balancer ID: %s for Name %s", lb.ID, name)
			return lb, nil
		}
	}
	return nil, ErrNotFound
}

func waitLoadbalancerActiveProvisioningStatus(ctx context.Context, client *gobizfly.Client, loadbalancerID string) (string, error) {
	backoff := wait.Backoff{
		Duration: loadbalancerActiveInitDelay,
		Factor:   loadbalancerActiveFactor,
		Steps:    loadbalancerActiveSteps,
	}
	var provisioningStatus string
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		lb, err := client.LoadBalancer.Get(ctx, loadbalancerID)
		if err != nil {
			klog.Infof("Cannot get status of loadbalancer %s", loadbalancerID)
			return false, err
		}
		provisioningStatus = lb.ProvisioningStatus
		if lb.ProvisioningStatus == activeStatus {
			return true, nil
		} else if lb.ProvisioningStatus == errorStatus {
			klog.Errorf("loadbalancer %s has gone into ERROR state", loadbalancerID)
			return true, fmt.Errorf("loadbalancer %s has gone into ERROR state", loadbalancerID)
		} else {
			return false, nil
		}
	})

	if err == wait.ErrWaitTimeout {
		err = fmt.Errorf("loadbalancer failed to go into ACTIVE provisioning status within allotted time")
	}
	return provisioningStatus, err
}

func waitLoadbalancerDeleted(ctx context.Context, client *gobizfly.Client, loadbalancerID string) error {
	backoff := wait.Backoff{
		Duration: loadbalancerDeleteInitDelay,
		Factor:   loadbalancerDeleteFactor,
		Steps:    loadbalancerDeleteSteps,
	}
	err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		_, err := client.LoadBalancer.Get(ctx, loadbalancerID)
		if err != nil {
			if cpoerrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})

	if err == wait.ErrWaitTimeout {
		err = fmt.Errorf("loadbalancer failed to delete within the allotted time")
	}

	return err
}

// getStringFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's value or a specified defaultSetting
func getStringFromServiceAnnotation(service *v1.Service, annotationKey string, defaultSetting string) string {
	klog.Infof("getStringFromServiceAnnotation(%v, %v, %v)", service, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		//if there is an annotation for this setting, set the "setting" var to it
		// annotationValue can be empty, it is working as designed
		// it makes possible for instance provisioning loadbalancer without floatingip
		klog.Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
		return annotationValue
	}
	//if there is no annotation, set "settings" var to the value from cloud config
	klog.Infof("Could not find a Service Annotation; falling back on default setting: %v = %v", annotationKey, defaultSetting)
	return defaultSetting
}

func getIntFromServiceAnnotation(service *v1.Service, annotationKey string) (int, bool) {
	intString := getStringFromServiceAnnotation(service, annotationKey, "")
	if len(intString) > 0 {
		annotationValue, err := strconv.Atoi(intString)
		if err == nil {
			klog.Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
			return annotationValue, true
		}
	}
	return 0, false
}

func (l *loadbalancers) createLoadBalancer(ctx context.Context, name string, networkType string, lbType string, vpcNetworkName string) (*gobizfly.LoadBalancer, error) {
	vpcNetworkId := ""
	if networkType == INTERNAL_NETWORK_TYPE {
		// find vpc network id by name
		vpcs, err := l.gclient.VPC.List(ctx)
		if err != nil {
			return nil, err
		}
		for _, vpc := range vpcs {
			if vpc.Name == vpcNetworkName {
				vpcNetworkId = vpc.ID
				break
			}
		}
	}
	lcr := gobizfly.LoadBalancerCreateRequest{
		Name:         name,
		NetworkType:  networkType,
		Type:         lbType,
		VPCNetworkID: vpcNetworkId,
	}
	loadbalancer, err := l.gclient.LoadBalancer.Create(ctx, &lcr)
	if err != nil {
		return nil, err
	}
	return loadbalancer, nil
}

func (l *loadbalancers) createListener(ctx context.Context, portIndex int, port int, protocol, lbName, lbID string) (*gobizfly.Listener, error) {
	// listenerProtocol := string(port.Protocol)
	listenerName := cutString(fmt.Sprintf("listener_%d_%s", portIndex, lbName))
	lcr := gobizfly.ListenerCreateRequest{
		Name:         &listenerName,
		Protocol:     protocol,
		ProtocolPort: port,
	}

	klog.Infof("Creating listener for port %d using protocol: %s", port, protocol)
	listener, err := l.gclient.Listener.Create(ctx, lbID, &lcr)
	if err != nil {
		klog.Errorf("failed to create listener for loadbalancer %s: %v", lbID, err)
		return nil, fmt.Errorf("failed to create listener for loadbalancer %s: %v", lbID, err)
	}

	klog.Infof("Listener %s created for loadbalancer %s", listener.ID, lbID)

	return listener, nil
}

func (l *loadbalancers) createPoolForListener(ctx context.Context, listener *gobizfly.Listener, portIndex int, lbID, lbName string, sessionPersistence *gobizfly.SessionPersistence, useProxyProtocol bool, isdefault bool) (*gobizfly.Pool, error) {
	poolProtocol := string(listener.Protocol)
	if useProxyProtocol {
		poolProtocol = PROXY_PROTOCOL
	}
	poolName := cutString(fmt.Sprintf("pool_%d_%s", portIndex, lbName))
	pcr := gobizfly.PoolCreateRequest{
		Name:               &poolName,
		Protocol:           poolProtocol,
		LBAlgorithm:        ROUND_ROBIN, // TODO use annotation for algorithm
		SessionPersistence: sessionPersistence,
		ListenerID:         listener.ID,
	}
	if isdefault == false {
		pcr.ListenerID = ""
	} else {
		pcr.ListenerID = listener.ID
	}

	klog.Infof("listener %v", pcr)
	klog.Infof("Creating pool for listener %s using protocol %s", listener.ID, poolProtocol)
	pool, err := l.gclient.Pool.Create(ctx, lbID, &pcr)
	if err != nil {
		klog.Errorf("error creating pool for listener %s: %v", listener.ID, err)
		return nil, fmt.Errorf("error creating pool for listener %s: %v", listener.ID, err)
	}
	return pool, nil
}

func getListenersByLoadBalancerID(ctx context.Context, client *gobizfly.Client, loadbalancerID string) ([]*gobizfly.Listener, error) {
	listeners, err := client.Listener.List(ctx, loadbalancerID, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	return listeners, nil
}

// get listener for a port or nil if does not exist
func getListenerForPort(existingListeners []*gobizfly.Listener, port v1.ServicePort) (*gobizfly.Listener, error) {
	for _, l := range existingListeners {
		if l.Protocol == toListenersProtocol(port.Protocol) && l.ProtocolPort == int(port.Port) {
			return l, nil
		}
	}
	return nil, ErrNotFound
}

func toListenersProtocol(protocol v1.Protocol) string {
	switch protocol {
	case v1.ProtocolTCP:
		return "TCP"
	default:
		return string(protocol)
	}
}

// Check if a member exists for node
func memberExists(members []*gobizfly.Member, addr string, port int) bool {
	for _, member := range members {
		if member.Address == addr && member.ProtocolPort == port {
			return true
		}
	}
	return false
}

func popListener(existingListeners []*gobizfly.Listener, id string) []*gobizfly.Listener {
	for i, existingListener := range existingListeners {
		if existingListener.ID == id {
			existingListeners[i] = existingListeners[len(existingListeners)-1]
			existingListeners = existingListeners[:len(existingListeners)-1]
			break
		}
	}

	return existingListeners
}

func popMember(members []*gobizfly.Member, addr string, port int) []*gobizfly.Member {
	for i, member := range members {
		if member.Address == addr && member.ProtocolPort == port {
			members[i] = members[len(members)-1]
			members = members[:len(members)-1]
		}
	}

	return members
}

// Get pool for a listener. A listener always has exactly one pool.
func getPoolByListenerID(ctx context.Context, client *gobizfly.Client, loadbalancerID string, listenerID string) (*gobizfly.Pool, error) {
	listenerPools := make([]*gobizfly.Pool, 0, 1)
	loadbalancerPools, err := client.Pool.List(ctx, loadbalancerID, &gobizfly.ListOptions{})

	if err != nil {
		return nil, err
	}
	if len(loadbalancerPools) == 0 {
		return nil, ErrNotFound
	}
	for _, p := range loadbalancerPools {
		for _, l := range p.Listeners {
			if l.ID == listenerID {
				listenerPools = append(listenerPools, p)
			}
		}
	}

	if len(listenerPools) == 0 {
		return nil, ErrNotFound
	}
	if len(listenerPools) > 1 {
		return nil, ErrMultipleResults
	}
	return listenerPools[0], nil
}

func getMembersByPoolID(ctx context.Context, client *gobizfly.Client, poolID string) ([]*gobizfly.Member, error) {
	members, err := client.Member.List(ctx, poolID, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	return members, nil
}

// The LB needs to be configured with instance addresses on the same
// subnet as the LB (aka opts.SubnetID).  Currently we're just
// guessing that the node's InternalIP is the right address.
// In case no InternalIP can be found, ExternalIP is tried.
// If neither InternalIP nor ExternalIP can be found an error is
// returned.
func nodeAddressForLB(node *v1.Node) (string, error) {
	addrs := node.Status.Addresses
	if len(addrs) == 0 {
		return "", ErrNoAddressFound
	}

	allowedAddrTypes := []v1.NodeAddressType{v1.NodeInternalIP, v1.NodeExternalIP}

	for _, allowedAddrType := range allowedAddrTypes {
		for _, addr := range addrs {
			if addr.Type == allowedAddrType {
				return addr.Address, nil
			}
		}
	}
	return "", ErrNoAddressFound
}

func deletePool(ctx context.Context, client *gobizfly.Client, poolID string) error {
	err := client.Pool.Delete(ctx, poolID)
	if err != nil {
		klog.Errorf("error deleting pool %s: %v", poolID, err)
		return err
	}
	return nil
}

func updateListenerDefaultPool(ctx context.Context, client *gobizfly.Client, poolID string, listenerID string) (*gobizfly.Listener, error) {
	payload := gobizfly.ListenerUpdateRequest{
		DefaultPoolID: &poolID,
	}
	listener, err := client.Listener.Update(ctx, listenerID, &payload)
	if err != nil {
		klog.Errorf("Updating error pool %s: %v", poolID, err)
		return nil, fmt.Errorf("Updating error pool %s: %v", poolID, err)
	}
	return listener, nil
}

func filterTargetNodes(apiService *v1.Service, nodes []*v1.Node) []*v1.Node {
	targetNodeLabels := getKeyValueFromServiceAnnotation(apiService, annotationLoadBalancerTargetNodeLabels)
	if len(targetNodeLabels) == 0 {
		return nodes
	}
	targetNodes := make([]*v1.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.Labels != nil && len(node.Labels) > 0 {
			allFiltersMatch := true

			for targetLabelKey, targetLabelValue := range targetNodeLabels {
				if nodeLabelValue, ok := node.Labels[targetLabelKey]; !ok || (nodeLabelValue != targetLabelValue && targetLabelValue != "") {
					allFiltersMatch = false
					break
				}
			}
			if allFiltersMatch {
				targetNodes = append(targetNodes, node)
			}
		}
	}
	return targetNodes
}

func getKeyValueFromServiceAnnotation(service *v1.Service, annotationKey string) map[string]string {
	klog.Infof("getKeyValueFromServiceAnnotation(%v, %v)", service, annotationKey)
	additionalTags := make(map[string]string)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		klog.Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
		annotationValueTrimmed := strings.TrimSpace(annotationValue)

		// Break up list of "Key1=Val,Key2=Val2"
		tagList := strings.Split(annotationValueTrimmed, ",")

		// Break up "Key=Val"
		for _, tagSet := range tagList {
			tag := strings.Split(strings.TrimSpace(tagSet), "=")

			// Accept "Key=val" or "Key=" or just "Key"
			if len(tag) >= 2 && len(tag[0]) != 0 {
				// There is a key and a value, so save it
				additionalTags[tag[0]] = tag[1]
			} else if len(tag) == 1 && len(tag[0]) != 0 {
				// Just "Key"
				additionalTags[tag[0]] = ""
			}
		}
	}
	return additionalTags
}

func memberExistsInCS(ctx context.Context, client *gobizfly.Client, serverID string) bool {
	server, err := client.Server.Get(ctx, serverID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false
		} else {
			return true
		}
	}
	if server != nil {
		return false
	} else {
		return true
	}
}

func batchUpdateMembers(ctx context.Context, client *gobizfly.Client, poolID string, lbID string, nodes []*v1.Node, port *v1.ServicePort, portIndex int, name string, when string) (int, error) {
	batchUpdateNodeList := []gobizfly.ExtendMemberUpdateRequest{}
	members, err := getMembersByPoolID(ctx, client, poolID)
	if err != nil {
		return 0, fmt.Errorf("error getting pool members %s: %s", poolID, err)
	}
	initialMembers := len(members)
	klog.Infof("Number of members in batchUpdate %v", initialMembers)

	if len(nodes) == 0 && len(members) > 0 {
		// When vpn server crashes, append nodes to batchUpdateNodeList if members still exist in CS
		for _, member := range members {
			memberExists := memberExistsInCS(ctx, client, member.ID)
			// If member exists in CS, then append to batchUpdateNodeList to maintaint the existsing members
			if memberExists {
				memberName := gobizfly.MemberUpdateRequest{
					Name: member.Name,
				}
				batchUpdateNodeList = append(batchUpdateNodeList, gobizfly.ExtendMemberUpdateRequest{
					MemberUpdateRequest: memberName,
					Address:             member.Address,
					ProtocolPort:        int(port.NodePort),
				})
			}
		}
	} else {
		for _, node := range nodes {
			addr, err := nodeAddressForLB(node)

			if err != nil {
				if errors.Is(err, ErrNoAddressFound) {
					// Node failure, do not create member
					klog.Warningf("Failed to create LB pool member for node %s: %s", node.Name, err)
					continue
				} else {
					klog.Errorf("error getting address for node %s: %s", node.Name, err)
					return 0, fmt.Errorf("error getting address for node %s: %s", node.Name, err)
				}
			}
			if !memberExists(members, addr, int(port.NodePort)) {
				member := gobizfly.MemberUpdateRequest{
					Name: cutString(fmt.Sprintf("member_%d_%s_%s", portIndex, node.Name, name)),
				}
				batchUpdateNodeList = append(batchUpdateNodeList, gobizfly.ExtendMemberUpdateRequest{
					MemberUpdateRequest: member,
					Address:             addr,
					ProtocolPort:        int(port.NodePort),
				})
			} else {
				member := gobizfly.MemberUpdateRequest{
					Name: cutString(fmt.Sprintf("member_%d_%s_%s", portIndex, node.Name, name)),
				}
				batchUpdateNodeList = append(batchUpdateNodeList, gobizfly.ExtendMemberUpdateRequest{
					MemberUpdateRequest: member,
					Address:             addr,
					ProtocolPort:        int(port.NodePort),
				})
				// After all members have been processed, remaining members are deleted as obsolete.
				members = popMember(members, addr, int(port.NodePort))
			}
		}
	}

	klog.Infof("Number of batchUpdateNodeList %v", len(batchUpdateNodeList))
	klog.Infof("batchUpdateNodeList %v", batchUpdateNodeList)
	klog.Infof("Batch creating members for pool %s when %s", poolID, when)
	// Calling batchUpdate
	err = client.Member.BatchUpdate(ctx, poolID, &gobizfly.BatchMemberUpdateRequest{
		Members: batchUpdateNodeList,
	})
	if err != nil {
		klog.Infof("error batch update LB pool members %s", err)
		return 0, fmt.Errorf("error batch update LB pool members %s", err)
	}

	provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, client, lbID)
	if err != nil {
		klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
		return 0, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
	}
	return len(batchUpdateNodeList) - initialMembers, nil
}

func (l *loadbalancers) changePoolProtocol(ctx context.Context, lbName string, useProxyProtocol bool, listener *gobizfly.Listener, portIndex int, port v1.ServicePort, lbID string, persistence *gobizfly.SessionPersistence, nodes []*v1.Node, oldPool *gobizfly.Pool) error {
	//create new pool
	new_pool, err := l.createPoolForListener(ctx, listener, portIndex, lbID, lbName, persistence, useProxyProtocol, false)
	klog.Infof("Pool created for listener %s: %s", listener.ID, new_pool.ID)
	if err != nil {
		return err
	}
	provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, lbID)
	if err != nil {
		klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating pool, current provisioning status %s", provisioningStatus)
		return fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating pool, current provisioning status %s", provisioningStatus)
	}

	// Create Batch update members
	when := "change protocol in ensuring loadbalancer"
	_, err = batchUpdateMembers(ctx, l.gclient, new_pool.ID, lbID, nodes, &port, portIndex, lbName, when)
	if err != nil {
		return err
	}

	// update listener
	klog.Infof("Update new poolID for listener")
	_, err = updateListenerDefaultPool(ctx, l.gclient, new_pool.ID, listener.ID)
	if err != nil {
		klog.Errorf("Update new poolID for listener failed: %s", err)
		return err
	}
	provisioningStatus, err = waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, lbID)
	if err != nil {
		klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
		return fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
	}

	// Delete old pool members
	klog.Infof("Delete old pool for listener")
	err = deletePool(ctx, l.gclient, oldPool.ID)
	if err != nil {
		return err
	}
	provisioningStatus, err = waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, lbID)
	if err != nil {
		klog.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
		return fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
	}

	return nil
}
