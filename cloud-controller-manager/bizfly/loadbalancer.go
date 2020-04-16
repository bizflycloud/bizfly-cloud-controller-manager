package bizfly

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/bizflycloud/gobizfly"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog"

	cpoerrors "k8s.io/cloud-provider-openstack/pkg/util/errors"
)

const (
	// loadbalancerActive* is configuration of exponential backoff for
	// going into ACTIVE loadbalancer provisioning status. Starting with 1
	// seconds, multiplying by 1.2 with each step and taking 19 steps at maximum
	// it will time out after 128s, which roughly corresponds to 120s
	loadbalancerActiveInitDelay = 1 * time.Second
	loadbalancerActiveFactor    = 1.2
	loadbalancerActiveSteps     = 19

	activeStatus = "ACTIVE"
	errorStatus  = "ERROR"

	// loadbalancerDelete* is configuration of exponential backoff for
	// waiting for delete operation to complete. Starting with 1
	// seconds, multiplying by 1.2 with each step and taking 13 steps at maximum
	// it will time out after 32s, which roughly corresponds to 30s
	loadbalancerDeleteInitDelay = 1 * time.Second
	loadbalancerDeleteFactor    = 1.2
	loadbalancerDeleteSteps     = 13

	annoBCLoadBalancerID = "kubernetes.bizflycloud.vn/load-balancer-id"

	annoBCLoadBalancerName = "kubernetes.bizflycloud.vn/load-balancer-name"

	annoBCListenerProtocol = "kubernetes.bizflycloud.vn/listener-protocol"

	annoBCLoadBalancerNetworkType = "kubernetes.bizflycloud.vn/load-balancer-network-type"
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
	name := l.GetLoadBalancerName(ctx, clusterName, service)
	loadbalancer, err := getLBByName(ctx, l.gclient, name)

	if err != nil {
		return nil, false, err
	}
	status := &v1.LoadBalancerStatus{}
	status.Ingress = []v1.LoadBalancerIngress{{IP: loadbalancer.VipAddress}}

	return status, true, nil
}

// cutString makes sure the string length doesn't exceed 255, which is usually the maximum string length in OpenStack.
func cutString(original string) string {
	ret := original
	if len(original) > 255 {
		ret = original[:255]
	}
	return ret
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
	klog.V(4).Infof("EnsureLoadBalancer(%s, %s)", clusterName, serviceName)

	if len(nodes) == 0 {
		return nil, fmt.Errorf("there are no available nodes for LoadBalancer service %s", serviceName)
	}

	ports := apiService.Spec.Ports
	if len(ports) == 0 {
		return nil, fmt.Errorf("no ports provided to openstack load balancer")
	}

	// Network type of load balancer: internal or external
	networkType := getStringFromServiceAnnotation(apiService, annoBCLoadBalancerNetworkType, "external")

	// Affinity Configuration for pool
	affinity := apiService.Spec.SessionAffinity
	var persistence *gobizfly.SessionPersistence
	switch affinity {
	case v1.ServiceAffinityNone:
		persistence = nil
	case v1.ServiceAffinityClientIP:
		persistence = &gobizfly.SessionPersistence{Type: "SOURCE_IP"}
	default:
		return nil, fmt.Errorf("unsupported load balancer affinity: %v", affinity)
	}

	// Check load balancer is exist or not
	name := l.GetLoadBalancerName(ctx, clusterName, apiService)
	loadbalancer, err := getLBByName(ctx, l.gclient, name)
	if err != nil {
		if err != ErrNotFound {
			return nil, fmt.Errorf("error getting loadbalancer for Service %s: %v", serviceName, err)
		}
		// Create new load balancer is the load balancer is not exist.
		klog.V(2).Infof("Creating loadbalancer %s", name)

		loadbalancer, err = l.createLoadBalancer(apiService, name, clusterName, networkType)
		if err != nil {
			return nil, fmt.Errorf("error creating loadbalancer %s: %v", name, err)
		}

	} else {
		klog.V(2).Infof("LoadBalancer %s already exists", loadbalancer.Name)
	}

	provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
	if err != nil {
		return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE, current provisioning status %s", provisioningStatus)
	}

	oldListeners, err := getListenersByLoadBalancerID(ctx, l.gclient, loadbalancer.ID)
	if err != nil {
		return nil, fmt.Errorf("error getting LB %s listeners: %v", loadbalancer.Name, err)
	}

	for portIndex, port := range ports {
		listener := getListenerForPort(oldListeners, port)

		if listener == nil {
			// Create a new listener
			listenerProtocol := string(port.Protocol)
			listenerName := cutString(fmt.Sprintf("listener_%d_%s", portIndex, name))
			lcr := gobizfly.ListenerCreateRequest{
				Name:         &listenerName,
				Protocol:     listenerProtocol,
				ProtocolPort: int(port.Port),
			}

			klog.V(4).Infof("Creating listener for port %d using protocol: %s", int(port.Port), listenerProtocol)
			listener, err := l.gclient.Listener.Create(ctx, loadbalancer.ID, &lcr)
			if err != nil {
				return nil, fmt.Errorf("failed to create listener for loadbalancer %s: %v", loadbalancer.ID, err)
			}

			klog.V(4).Infof("Listener %s created for loadbalancer %s", listener.ID, loadbalancer.ID)

		} else {
			// Update old listener
			// listenerChanged := false
			// updateOpts := gobizfly.ListenerUpdateRequest{}

			// TODO update listener
		}
		// After all ports have been processed, remaining listeners are removed as obsolete.
		// Pop valid listeners.
		oldListeners = popListener(oldListeners, listener.ID)

		pool, err := getPoolByListenerID(ctx, l.gclient, loadbalancer.ID, listener.ID)

		if err != nil && err != ErrNotFound {
			return nil, fmt.Errorf("error getting pool for listener %s: %v", listener.ID, err)
		}

		if pool == nil {
			// Create a new pool

			// use protocol of listener
			poolProtocol := string(listener.Protocol)
			poolName := cutString(fmt.Sprintf("pool_%d_%s", portIndex, name))
			pcr := gobizfly.PoolCreateRequest{
				Name:               &poolName,
				Protocol:           poolProtocol,
				LBAlgorithm:        "ROUND_ROBIN", // TODO use annotation for algorithm
				SessionPersistence: persistence,
				ListenerID:         &listener.ID,
			}

			klog.V(4).Infof("Creating pool for listener %s using protocol %s", listener.ID, poolProtocol)
			pool, err = l.gclient.Pool.Create(ctx, loadbalancer.ID, &pcr)
			if err != nil {
				return nil, fmt.Errorf("error creating pool for listener %s: %v", listener.ID, err)
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating pool, current provisioning status %s", provisioningStatus)
			}
		}

		klog.V(4).Infof("Pool created for listener %s: %s", listener.ID, pool.ID)

		members, err := getMembersByPoolID(ctx, l.gclient, pool.ID)
		if err != nil && !cpoerrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting pool members %s: %v", pool.ID, err)
		}
		for _, node := range nodes {
			addr, err := nodeAddressForLB(node)

			if err != nil {
				if err == ErrNotFound {
					// Node failure, do not create member
					klog.Warningf("Failed to create LB pool member for node %s: %v", node.Name, err)
					continue
				} else {
					return nil, fmt.Errorf("error getting address for node %s: %v", node.Name, err)
				}
			}
			if !memberExists(members, addr, int(port.NodePort)) {
				klog.V(4).Infof("Creating member for pool %s", pool.ID)

				_, err := l.gclient.Member.Create(ctx, pool.ID, &gobizfly.MemberCreateRequest{
					Name:         cutString(fmt.Sprintf("member_%d_%s_%s", portIndex, node.Name, name)),
					ProtocolPort: int(port.NodePort),
					Address:      addr,
				})
				if err != nil {
					return nil, fmt.Errorf("error creating LB pool member for node: %s, %v", node.Name, err)
				}

				provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
				if err != nil {
					return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
				}
			} else {
				// After all members have been processed, remaining members are deleted as obsolete.
				members = popMember(members, addr, int(port.NodePort))
			}

			klog.V(4).Infof("Ensured pool %s has member for %s at %s", pool.ID, node.Name, addr)

			// Delete obsolete members for this pool
			for _, member := range members {
				klog.V(4).Infof("Deleting obsolete member %s for pool %s address %s", member.ID, pool.ID, member.Address)
				err := l.gclient.Member.Delete(ctx, pool.ID, member.ID)
				if err != nil && !cpoerrors.IsNotFound(err) {
					return nil, fmt.Errorf("error deleting obsolete member %s for pool %s address %s: %v", member.ID, pool.ID, member.Address, err)
				}
				provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
				if err != nil {
					return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting member, current provisioning status %s", provisioningStatus)
				}
			}
			// TODO Create health Monitor for pool
		}

	}
	// All remaining listeners are obsolete, delete
	for _, listener := range oldListeners {
		klog.V(4).Infof("Deleting obsolete listener %s:", listener.ID)
		// get pool for listener
		pool, err := getPoolByListenerID(ctx, l.gclient, loadbalancer.ID, listener.ID)
		if err != nil && err != ErrNotFound {
			return nil, fmt.Errorf("error getting pool for obsolete listener %s: %v", listener.ID, err)
		}
		if pool != nil {
			// get and delete monitor
			// TODO: delete monitor later when monitor entity is added
			// get and delete pool members
			members, err := getMembersByPoolID(ctx, l.gclient, pool.ID)
			if err != nil && !cpoerrors.IsNotFound(err) {
				return nil, fmt.Errorf("error getting members for pool %s: %v", pool.ID, err)
			}
			if members != nil {
				for _, member := range members {
					klog.V(4).Infof("Deleting obsolete member %s for pool %s address %s", member.ID, pool.ID, member.Address)
					err := l.gclient.Member.Delete(ctx, pool.ID, member.ID)
					if err != nil && !cpoerrors.IsNotFound(err) {
						return nil, fmt.Errorf("error deleting obsolete member %s for pool %s address %s: %v", member.ID, pool.ID, member.Address, err)
					}
					provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
					if err != nil {
						return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting member, current provisioning status %s", provisioningStatus)
					}
				}
			}
			klog.V(4).Infof("Deleting obsolete pool %s for listener %s", pool.ID, listener.ID)
			// delete pool
			err = l.gclient.Pool.Delete(ctx, pool.ID)
			if err != nil && !cpoerrors.IsNotFound(err) {
				return nil, fmt.Errorf("error deleting obsolete pool %s for listener %s: %v", pool.ID, listener.ID, err)
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
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
			return nil, fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting listener, current provisioning status %s", provisioningStatus)
		}
		klog.V(2).Infof("Deleted obsolete listener: %s", listener.ID)
	}

	status := &v1.LoadBalancerStatus{}
	status.Ingress = []v1.LoadBalancerIngress{{IP: loadbalancer.VipAddress}}

	return status, nil
}

// UpdateLoadBalancer updates hosts under the specified load balancer.
// Implementations must treat the *v1.Service and *v1.Node
// parameters as read-only and not modify them.
// Parameter 'clusterName' is the name of the cluster as presented to kube-controller-manager
func (l *loadbalancers) UpdateLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) error {
	serviceName := fmt.Sprintf("%s/%s", service.Namespace, service.Name)
	klog.V(4).Infof("UpdateLoadBalancer(%v, %s, %v)", clusterName, serviceName, nodes)

	ports := service.Spec.Ports
	if len(ports) == 0 {
		return fmt.Errorf("no ports provided to bizflycloud load balancer")
	}

	name := l.GetLoadBalancerName(ctx, clusterName, service)
	lb, err := getLBByName(ctx, l.gclient, name)

	if err != nil {
		return err
	}

	if lb == nil {
		return fmt.Errorf("loadbalancer does not exist for Service %s", serviceName)
	}

	type portKey struct {
		Protocol string
		Port     int
	}
	var listenerIDs []string
	lbListeners := make(map[portKey]*gobizfly.Listener)
	listeners, err := getListenersByLoadBalancerID(ctx, l.gclient.Client, lb.ID)
	if err != nil {
		return fmt.Errorf("error getting listeners for LB %s: %v", lb.ID, err)
	}
	
	for _, l := range allListeners {
		key := portKey{Protocol: string(l.Protocol), Port: int(l.ProtocolPort)}
		lbListeners[key] = l
		listenerIDs = append(listenerIDs, l.ID)
	}

	pools, err := getPoolsByLoadBalancerID(ctx, l.gclient, lb.ID)
	if err != nil {
		return err
	}
	addrs := make(map[string]*v1.Node)
	for _, node := range nodes {
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
			return fmt.Errorf("loadbalancer %s does not contain required listener for port %d and protocol %s", loadbalancer.ID, port.Port, port.Protocol)
		}

		// Get pool associated with this listener
		pool, ok := lbPools[listener.ID]
		if !ok {
			return fmt.Errorf("loadbalancer %s does not contain required pool for listener %s", loadbalancer.ID, listener.ID)
		}

		// Find existing pool members (by address) for this port
		getMembers, err := getMembersByPoolID(ctx, l.gclient, pool.ID)
		if err != nil {
			return fmt.Errorf("error getting pool members %s: %v", pool.ID, err)
		}
		members := make(map[string]*gobizfly.Member)
		for _, member := range getMembers {
			members[member.Address] = member
		}

		// Add any new members for this port
		for addr, node := range addrs {
			if _, ok := members[addr]; ok && members[addr].ProtocolPort == int(port.NodePort) {
				// Already exists, do not create member
				continue
			}
			_, err := l.gclient.Member.Create(ctx, pool.ID, &gobizfly.MemberCreateRequest{
				Name:         cutString(fmt.Sprintf("member_%d_%s_%s_", portIndex, node.Name, loadbalancer.Name)),
				Address:      addr,
				ProtocolPort: int(port.NodePort)
			})
			if err != nil {
				return err
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				return fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after creating member, current provisioning status %s", provisioningStatus)
			}
		}

		// Remove any old members for this port
		for _, member := range members {
			if _, ok := addrs[member.Address]; ok && member.ProtocolPort == int(port.NodePort) {
				// Still present, do not delete member
				continue
			}
			err = l.gclient.Member.Delete(ctx, pool.ID, member.ID)
			if err != nil && !cpoerrors.IsNotFound(err) {
				return err
			}
			provisioningStatus, err := waitLoadbalancerActiveProvisioningStatus(ctx, l.gclient, loadbalancer.ID)
			if err != nil {
				return fmt.Errorf("timeout when waiting for loadbalancer to be ACTIVE after deleting member, current provisioning status %s", provisioningStatus)
			}
		}
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
	klog.V(4).Infof("EnsureLoadBalancerDeleted(%s, %s)", clusterName, serviceName)

	name := l.GetLoadBalancerName(ctx, clusterName, service)
	lb, err := getLBByName(ctx, l.gclient, name)
	if err != nil {
		err
	}
	err := l.gclient.LoadBalancer.Delete(ctx, &gobizfly.LoadBalancerDeleteRequest{true, lb.ID})
	if err != nil {
		return err
	}
	err = waitLoadbalancerDeleted(lbaas.lb, loadbalancer.ID)
	if err != nil {
		return fmt.Errorf("failed to delete loadbalancer: %v", err)
	}
	return nil
}

func getLBByName(ctx context.Context, client *gobizfly.Client, name string) (*gobizfly.LoadBalancer, error) {
	loadbalancers, err := client.LoadBalancer.List(ctx, &gobizfly.ListOptions{})
	if err != nil {
		fmt.Errorf("Cannot get loadbalancers in your account: %v", err)
		return nil, err
	}
	for _, lb := range loadbalancers {
		if lb.Name == name {
			klog.V(4).Infof("Selected Load Balancer ID: %s for Name %s", lb.ID, name)
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
			klog.V(4).Infof("Cannot get status of loadbalancer %s", loadbalancerID)
			return false, err
		}
		provisioningStatus = lb.ProvisioningStatus
		if lb.ProvisioningStatus == activeStatus {
			return true, nil
		} else if lb.ProvisioningStatus == errorStatus {
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

//getStringFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's value or a specified defaultSetting
func getStringFromServiceAnnotation(service *v1.Service, annotationKey string, defaultSetting string) string {
	klog.V(4).Infof("getStringFromServiceAnnotation(%v, %v, %v)", service, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		//if there is an annotation for this setting, set the "setting" var to it
		// annotationValue can be empty, it is working as designed
		// it makes possible for instance provisioning loadbalancer without floatingip
		klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
		return annotationValue
	}
	//if there is no annotation, set "settings" var to the value from cloud config
	klog.V(4).Infof("Could not find a Service Annotation; falling back on default setting: %v = %v", annotationKey, defaultSetting)
	return defaultSetting
}

func getIntFromServiceAnnotation(service *v1.Service, annotationKey string) (int, bool) {
	intString := getStringFromServiceAnnotation(service, annotationKey, "")
	if len(intString) > 0 {
		annotationValue, err := strconv.Atoi(intString)
		if err == nil {
			klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
			return annotationValue, true
		}
	}
	return 0, false
}

func (l *loadbalancers) createLoadBalancer(apiService *v1.Service, name string, clusterName string, networkType string) (*gobizfly.LoadBalancer, error) {

	return nil, nil
}

func getListenersByLoadBalancerID(ctx context.Context, client *gobizfly.Client, loadbalancerID string) ([]*gobizfly.Listener, error) {
	listeners, err := client.Listener.List(ctx, loadbalancerID, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	return listeners, nil
}

func getPoolsByLoadBalancerID(ctx context.Context, client *gobizfly.Client, loadbalancerID string) ([]*gobizfly.Pool, error) {
	pools, err := client.Pool.List(ctx, loadbalancerID, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	return pools, nil
}

// get listener for a port or nil if does not exist
func getListenerForPort(existingListeners []*gobizfly.Listener, port v1.ServicePort) *gobizfly.Listener {
	for _, l := range existingListeners {
		if l.Protocol == toListenersProtocol(port.Protocol) && l.ProtocolPort == int(port.Port) {
			return l
		}
	}
	return nil
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
		if cpoerrors.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
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
	} else if len(listenerPools) > 1 {
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
