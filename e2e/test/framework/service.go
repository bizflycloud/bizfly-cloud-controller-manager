package framework

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/util/retry"

	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (i *lbInvocation) createOrUpdateService(selector, annotations map[string]string, ports []core.ServicePort, isSessionAffinityClientIP, isCreate bool, isLB bool) error {
	var sessionAffinity core.ServiceAffinity = "None"
	var spec core.ServiceSpec
	if isSessionAffinityClientIP {
		sessionAffinity = "ClientIP"
	}
	spec = core.ServiceSpec{
		Ports:           ports,
		Selector:        selector,
		SessionAffinity: sessionAffinity,
	}
	if isLB {
		spec.Type = core.ServiceTypeLoadBalancer
	} else {
		spec.Type = core.ServiceTypeClusterIP
	}
	svc := &core.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        TestServerResourceName,
			Namespace:   i.Namespace(),
			Annotations: annotations,
			Labels: map[string]string{
				"app": "test-server-" + i.app,
			},
		},
		Spec: spec,
	}

	service := i.kubeClient.CoreV1().Services(i.Namespace())
	if isCreate {
		_, err := service.Create(context.TODO(), svc, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	} else {
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			options := metav1.GetOptions{}
			resource, err := service.Get(context.TODO(), TestServerResourceName, options)
			if err != nil {
				return err
			}
			svc.ObjectMeta.ResourceVersion = resource.ResourceVersion
			svc.Spec.ClusterIP = resource.Spec.ClusterIP
			_, err = service.Update(context.TODO(), svc, metav1.UpdateOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func (i *lbInvocation) CreateService(selector, annotations map[string]string, ports []core.ServicePort, isSessionAffinityClientIP bool) error {
	return i.createOrUpdateService(selector, annotations, ports, isSessionAffinityClientIP, true, true)
}

func (i *lbInvocation) GetServiceEndpoints() ([]core.EndpointAddress, error) {
	ep, err := i.kubeClient.CoreV1().Endpoints(i.Namespace()).Get(context.TODO(), TestServerResourceName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if len(ep.Subsets) == 0 {
		return nil, fmt.Errorf("no service endpoints found for %s", TestServerResourceName)
	}
	return ep.Subsets[0].Addresses, err
}

func (i *lbInvocation) GetServiceWatcher() (watch.Interface, error) {
	var timeoutSeconds int64 = 300
	watcher, err := i.kubeClient.CoreV1().Events(i.Namespace()).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "involvedObject.kind=Service",
		Watch:          true,
		TimeoutSeconds: &timeoutSeconds,
	})
	if err != nil {
		return nil, err
	}
	return watcher, nil
}

func (i *lbInvocation) DeleteService() error {
	return i.kubeClient.CoreV1().Services(i.Namespace()).Delete(context.TODO(), TestServerResourceName, metav1.DeleteOptions{})
}

func (i *lbInvocation) GetLoadBalancerIps() ([]string, error) {
	svc, err := i.kubeClient.CoreV1().Services(i.Namespace()).Get(context.TODO(), TestServerResourceName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	var serverAddr []string
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if len(svc.Spec.Ports) > 0 {
			for _, port := range svc.Spec.Ports {
				if port.NodePort > 0 {
					serverAddr = append(serverAddr, fmt.Sprintf("%s:%d", ingress.IP, port.Port))
				}
			}
		}
	}
	if serverAddr == nil {
		return nil, fmt.Errorf("failed to get Status.LoadBalancer.Ingress for service %s/%s", TestServerResourceName, i.Namespace())
	}
	return serverAddr, nil
}

func (i *lbInvocation) UpdateService(selector, annotations map[string]string, ports []core.ServicePort, isSessionAffinityClientIP bool, isLB bool) error {
	err := i.deleteEvents()
	if err != nil {
		return err
	}
	return i.createOrUpdateService(selector, annotations, ports, isSessionAffinityClientIP, false, isLB)
}

func (i *lbInvocation) deleteEvents() error {
	return i.kubeClient.CoreV1().Events(i.Namespace()).DeleteCollection(context.TODO(), metav1.DeleteOptions{}, metav1.ListOptions{FieldSelector: "involvedObject.kind=Service"})
}
