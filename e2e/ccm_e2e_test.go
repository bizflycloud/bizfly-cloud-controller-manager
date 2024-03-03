package e2e_test

import (
	"e2e_test/test/framework"
	"fmt"
	"github.com/bizflycloud/gobizfly"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/watch"
)

func EnsuredService(isDeleted bool) types.GomegaMatcher {
	var ensureState string
	if isDeleted {
		ensureState = "DeletedLoadBalancer"
	} else {
		ensureState = "EnsuredLoadBalancer"
	}
	return And(
		WithTransform(func(e watch.Event) (string, error) {
			event, ok := e.Object.(*core.Event)
			if !ok {
				return "", fmt.Errorf("failed to poll event")
			}
			fmt.Println(event.Reason)
			return event.Reason, nil
		}, Equal(ensureState)),
	)
}

var _ = Describe("CCM E2E Tests", func() {
	var (
		err     error
		f       *framework.Invocation
		workers []string
	)

	const (
		bizflyProxyProtocol = "kubernetes.bizflycloud.vn/enable-proxy-protocol"
		bizflyNetworkType   = "kubernetes.bizflycloud.vn/load-balancer-network-type"
		bizflyNodeLabel     = "kubernetes.bizflycloud.vn/target-node-labels"
	)

	BeforeEach(func() {
		f = root.Invoke()
		workers, err = f.GetNodeList()
		Expect(err).NotTo(HaveOccurred())
		Expect(len(workers)).Should(BeNumerically(">=", 2))
	})

	ensureServiceLoadBalancer := func(isDeleted bool) {
		watcher, err := f.LoadBalancer.GetServiceWatcher()
		Expect(err).NotTo(HaveOccurred())
		Eventually(watcher.ResultChan()).Should(Receive(EnsuredService(isDeleted)))
	}

	createPodWithLabel := func(pods []string, ports []core.ContainerPort, image string, labels map[string]string, selectNode bool) {
		for i, pod := range pods {
			p := f.LoadBalancer.GetPodObject(pod, image, ports, labels)
			if selectNode {
				p = f.LoadBalancer.SetNodeSelector(p, workers[i])
			}
			Expect(f.LoadBalancer.CreatePod(p)).ToNot(BeNil())
			Eventually(f.LoadBalancer.GetPod).WithArguments(p.ObjectMeta.Name, f.LoadBalancer.Namespace()).Should(HaveField("Status.Phase", Equal(core.PodRunning)))
		}
	}

	deletePods := func(pods []string) {
		for _, pod := range pods {
			Expect(f.LoadBalancer.DeletePod(pod)).NotTo(HaveOccurred())
		}
	}

	deleteService := func() {
		Expect(f.LoadBalancer.DeleteService()).NotTo(HaveOccurred())
	}

	createServiceWithSelector := func(selector map[string]string, ports []core.ServicePort, isSessionAffinityClientIP bool, isDelete bool) {
		Expect(f.LoadBalancer.CreateService(selector, nil, ports, isSessionAffinityClientIP)).NotTo(HaveOccurred())
		Eventually(f.LoadBalancer.GetServiceEndpoints).Should(Not(BeEmpty()))
		ensureServiceLoadBalancer(isDelete)
	}

	createServiceWithAnnotations := func(labels, annotations map[string]string, ports []core.ServicePort, isSessionAffinityClientIP bool, isDelete bool) {
		Expect(f.LoadBalancer.CreateService(labels, annotations, ports, isSessionAffinityClientIP)).NotTo(HaveOccurred())
		Eventually(f.LoadBalancer.GetServiceEndpoints).Should(Not(BeEmpty()))
		ensureServiceLoadBalancer(isDelete)
	}

	updateServiceWithAnnotations := func(labels, annotations map[string]string, ports []core.ServicePort, isSessionAffinityClientIP bool, isLB bool) {
		Expect(f.LoadBalancer.UpdateService(labels, annotations, ports, isSessionAffinityClientIP, isLB)).NotTo(HaveOccurred())
		Eventually(f.LoadBalancer.GetServiceEndpoints).Should(Not(BeEmpty()))
		if isLB {
			ensureServiceLoadBalancer(false)
		} else {
			ensureServiceLoadBalancer(true)
		}
	}

	Describe("Test", func() {
		Context("Create", func() {
			AfterEach(func() {
				err := root.Recycle()
				Expect(err).NotTo(HaveOccurred())
			})
			Context("Load Balancer External", func() {
				var (
					pods   []string
					labels map[string]string
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts := []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer",
					}

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithSelector(labels, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should reach all pods", func() {
					var eps []string
					var lbId string
					var members int
					var listeners []*gobizfly.Listener
					var pools []*gobizfly.Pool
					Eventually(func() error {
						eps, err = f.LoadBalancer.GetLoadBalancerIps()
						fmt.Println(eps)
						return err
					}).Should(BeNil())
					Eventually(func() error {
						lbId, err = f.GetLBByName(ctx, clusterName, framework.TestServerResourceName)
						fmt.Println("lbID: " + lbId)
						return err
					}).Should(BeNil())
					Eventually(func() error {
						listeners, err = f.GetListners(ctx, lbId)
						fmt.Println("Listeners %i", len(listeners))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						pools, err = f.GetPools(ctx, lbId)
						fmt.Println("Pools %i", len(pools))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						members, err = f.CountMembersByPools(ctx, pools)
						fmt.Println("Members %i", members)
						return err
					}).Should(BeNil())
					By("Checking TCP Response")
					Eventually(framework.GetResponseFromCurl).WithArguments(eps[0]).Should(ContainSubstring("nginx"))
					Eventually(lbId).ShouldNot(Equal(""))
					By("Checking numbers of Listners")
					Eventually(len(listeners)).Should(Equal(2))
					By("Checking numbers of Pools")
					Eventually(len(pools)).Should(Equal(2))
					By("Checking numbers of Members")
					Eventually(members).Should(Equal(4))
				})
			})

			Context("Load Balancer Proxy", func() {
				var (
					pods        []string
					labels      map[string]string
					annotations = map[string]string{}
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts := []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer",
					}
					annotations[bizflyProxyProtocol] = "true"

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithAnnotations(labels, annotations, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should have proxy protocol for pools", func() {
					var eps []string
					var lbId string
					var members int
					var listeners []*gobizfly.Listener
					var pools []*gobizfly.Pool
					Eventually(func() error {
						eps, err = f.LoadBalancer.GetLoadBalancerIps()
						fmt.Println(eps)
						return err
					}).Should(BeNil())
					Eventually(func() error {
						lbId, err = f.GetLBByName(ctx, clusterName, framework.TestServerResourceName)
						fmt.Println("lbID: " + lbId)
						return err
					}).Should(BeNil())
					Eventually(func() error {
						listeners, err = f.GetListners(ctx, lbId)
						fmt.Println("Listeners %i", len(listeners))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						pools, err = f.GetPools(ctx, lbId)
						fmt.Println("Pools %i", len(pools))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						members, err = f.CountMembersByPools(ctx, pools)
						fmt.Println("Members %i", members)
						return err
					}).Should(BeNil())
					Eventually(lbId).ShouldNot(Equal(""))
					By("Checking numbers of Listners")
					Eventually(len(listeners)).Should(Equal(2))
					By("Checking numbers of Pools")
					Eventually(len(pools)).Should(Equal(2))
					By("Checking numbers of Members")
					Eventually(members).Should(Equal(4))
					By("Checking Pool Protocol")
					Eventually(pools[0].Protocol).Should(Equal("PROXY"))
					Eventually(pools[1].Protocol).Should(Equal("PROXY"))
				})
			})

			Context("Load Balancer Internal", func() {
				var (
					pods        []string
					labels      map[string]string
					annotations = map[string]string{}
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts := []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer",
					}
					annotations[bizflyNetworkType] = "internal"

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithAnnotations(labels, annotations, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should have internal network type", func() {
					var lb *gobizfly.LoadBalancer
					var lbId string
					var members int
					var listeners []*gobizfly.Listener
					var pools []*gobizfly.Pool
					Eventually(func() error {
						lb, err = f.GetLB(ctx, clusterName, framework.TestServerResourceName)
						lbId = lb.ID
						return err
					}).Should(BeNil())
					Eventually(func() error {
						listeners, err = f.GetListners(ctx, lbId)
						fmt.Println("Listeners %i", len(listeners))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						pools, err = f.GetPools(ctx, lbId)
						fmt.Println("Pools %i", len(pools))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						members, err = f.CountMembersByPools(ctx, pools)
						fmt.Println("Members %i", members)
						return err
					}).Should(BeNil())
					Eventually(lbId).ShouldNot(Equal(""))
					By("Checking Load Balancer's network type")
					Eventually(lb.NetworkType).Should(Equal("internal"))
					By("Checking numbers of Listners")
					Eventually(len(listeners)).Should(Equal(2))
					By("Checking numbers of Pools")
					Eventually(len(pools)).Should(Equal(2))
					By("Checking numbers of Members")
					Eventually(members).Should(Equal(4))
					By("Checking Pool Protocol")
				})
			})

			Context("Load Balancer target node label", func() {
				var (
					pods        []string
					labels      map[string]string
					annotations = map[string]string{}
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts := []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer",
					}
					annotations[bizflyNodeLabel] = "test-ccm=node01"

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithAnnotations(labels, annotations, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should have internal network type", func() {
					var lb *gobizfly.LoadBalancer
					var lbId string
					var members int
					var listeners []*gobizfly.Listener
					var pools []*gobizfly.Pool
					Eventually(func() error {
						lb, err = f.GetLB(ctx, clusterName, framework.TestServerResourceName)
						lbId = lb.ID
						return err
					}).Should(BeNil())
					Eventually(func() error {
						listeners, err = f.GetListners(ctx, lbId)
						fmt.Println("Listeners %i", len(listeners))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						pools, err = f.GetPools(ctx, lbId)
						fmt.Println("Pools %i", len(pools))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						members, err = f.CountMembersByPools(ctx, pools)
						fmt.Println("Members %i", members)
						return err
					}).Should(BeNil())
					Eventually(lbId).ShouldNot(Equal(""))
					By("Checking numbers of Listners")
					Eventually(len(listeners)).Should(Equal(2))
					By("Checking numbers of Pools")
					Eventually(len(pools)).Should(Equal(2))
					By("Checking numbers of Members")
					Eventually(members).Should(Equal(2))
					By("Checking Pool Protocol")
				})
			})
		})

		Context("Update", func() {
			AfterEach(func() {
				err := root.Recycle()
				Expect(err).NotTo(HaveOccurred())
			})
			Context("Load Balancer Proxy Protocol", func() {
				var (
					pods         []string
					labels       map[string]string
					servicePorts []core.ServicePort
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts = []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer-change-type",
					}

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithSelector(labels, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should have change from TCP to PROXY for every pools", func() {
					By("Update pool protocol from TCP PROXY")
					updateServiceWithAnnotations(labels, map[string]string{
						bizflyProxyProtocol: "true",
					}, servicePorts, false, true)
					var lb *gobizfly.LoadBalancer
					var lbId string
					var members int
					var listeners []*gobizfly.Listener
					var pools []*gobizfly.Pool
					Eventually(func() error {
						lb, err = f.GetLB(ctx, clusterName, framework.TestServerResourceName)
						lbId = lb.ID
						return err
					}).Should(BeNil())
					Eventually(func() error {
						listeners, err = f.GetListners(ctx, lbId)
						fmt.Println("Listeners %i", len(listeners))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						pools, err = f.GetPools(ctx, lbId)
						fmt.Println("Pools %i", len(pools))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						members, err = f.CountMembersByPools(ctx, pools)
						fmt.Println("Members %i", members)
						return err
					}).Should(BeNil())
					Eventually(lbId).ShouldNot(Equal(""))
					By("Checking numbers of Listners")
					Eventually(len(listeners)).Should(Equal(2))
					By("Checking numbers of Pools")
					Eventually(len(pools)).Should(Equal(2))
					By("Checking numbers of Members")
					Eventually(members).Should(Equal(4))
					By("Checking Pool Protocol")
					Eventually(pools[0].Protocol).Should(Equal("PROXY"))
					Eventually(pools[1].Protocol).Should(Equal("PROXY"))
				})
			})

			Context("Load Balancer Target Node Label", func() {
				var (
					pods         []string
					labels       map[string]string
					servicePorts []core.ServicePort
					annotations  map[string]string
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts = []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer-change-type",
					}
					annotations = map[string]string{
						bizflyNodeLabel: "test-ccm=node01",
					}

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithAnnotations(labels, annotations, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should have change from TCP to PROXY for every pools", func() {
					var lb *gobizfly.LoadBalancer
					var lbId string
					var oldMembers []*gobizfly.Member
					var newMembers []*gobizfly.Member
					var pools []*gobizfly.Pool
					Eventually(func() error {
						lb, err = f.GetLB(ctx, clusterName, framework.TestServerResourceName)
						lbId = lb.ID
						return err
					}).Should(BeNil())
					Eventually(func() error {
						pools, err = f.GetPools(ctx, lbId)
						fmt.Println("Pools %i", len(pools))
						return err
					}).Should(BeNil())
					Eventually(func() error {
						oldMembers, err = f.GetMembersByPools(ctx, pools)
						fmt.Println("Members %i", len(oldMembers))
						return err
					}).Should(BeNil())

					By("Update pool protocol from TCP PROXY")
					updateServiceWithAnnotations(labels, map[string]string{
						bizflyNodeLabel: "test-ccm=node02",
					}, servicePorts, false, true)

					Eventually(func() error {
						newMembers, err = f.GetMembersByPools(ctx, pools)
						fmt.Println("Members %i", len(newMembers))
						return err
					}).Should(BeNil())
					By("Checking change in targeted node")
					Eventually(newMembers[0].Name).ShouldNot(Equal(oldMembers[0].Name))
				})
			})
		})

		Context("Delete", func() {
			AfterEach(func() {
				err := root.Recycle()
				Expect(err).NotTo(HaveOccurred())
			})
			Context("Load Balancer", func() {
				var (
					pods         []string
					labels       map[string]string
					servicePorts []core.ServicePort
				)

				BeforeEach(func() {
					pods = []string{"test-pod-1", "test-pod-2"}
					ports := []core.ContainerPort{
						{
							Name:          "http-1",
							ContainerPort: 8080,
						},
					}
					servicePorts = []core.ServicePort{
						{
							Name:       "http-1",
							Port:       80,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
						{
							Name:       "https-1",
							Port:       443,
							TargetPort: intstr.FromInt(80),
							Protocol:   "TCP",
						},
					}
					labels = map[string]string{
						"app": "test-loadbalancer-change-type",
					}

					By("Creating Pods")
					createPodWithLabel(pods, ports, framework.TestServerImage, labels, true)

					By("Creating Service")
					createServiceWithSelector(labels, servicePorts, false, false)
				})

				AfterEach(func() {
					By("Deleting the Pods")
					deletePods(pods)

					By("Deleting the Service")
					deleteService()
				})

				It("Should be deleted", func() {
					By("Update service from LoadBalancer to ClusterIP")
					updateServiceWithAnnotations(labels, map[string]string{}, servicePorts, false, false)
					By("Load Balancer is deleted")
					Eventually(func() error {
						_, err := f.GetLB(ctx, clusterName, framework.TestServerResourceName)
						return err
					}).ShouldNot(BeNil())
				})
			})

		})

	})
})
