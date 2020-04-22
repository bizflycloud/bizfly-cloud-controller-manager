# Kubernetes Cloud Controller Manager for BizFlyCloud

`bizfly-cloud-controller-manager` is the Kubernetes cloud controller implmentation for BizFlyCloud. Read more about cloud controller managers [here](https://kubernetes.io/docs/tasks/administer-cluster/running-cloud-controller/). Running `bizfly-cloud-controller-manager` allows you to leverage many of the cloud provider features offered by BizFlyCloud on your kubernetes clusters.

# Getting Started

### Requirements

At the current state of Kubernetes, running cloud controller manager requires a few things. Please read through the requirements carefully as they are critical to running cloud controller manager on a Kubernetes cluster on BizFlyCloud.


### Parameters

This section outlines parameters that can be passed to the cloud controller manager binary.

#### --cloud-provider=external


All `kubelet`s in your cluster **MUST** set the flag `--cloud-provider=external`. `kube-apiserver` and `kube-controller-manager` must **NOT** set the flag `--cloud-provider` which will default them to use no cloud provider natively.

**WARNING**: setting `--cloud-provider=external` will taint all nodes in a cluster with `node.cloudprovider.kubernetes.io/uninitialized`, it is the responsibility of cloud controller managers to untaint those nodes once it has finished initializing them. This means that most pods will be left unscheduable until the cloud controller manager is running.

In the future, `--cloud-provider=external` will be the default. Learn more about the future of cloud providers in Kubernetes [here](https://github.com/kubernetes/community/blob/master/contributors/design-proposals/cloud-provider/cloud-provider-refactoring.md).


### Deployment

- Update file `manifest/secret.yaml` with your email and password. *Note: In the near future, we will use another method for authentication.*

```
apiVersion: v1
kind: Secret
metadata:
  name: bizflycloud
  namespace: kube-system
stringData:
  email: "youremail@example.com"
  password: "yourPassWORD"
```

- Run this command from the root of this repo:

```
kubectl apply -f manifest/secret.yaml
```

You should now see the bizflycloud secret in the kube-system namespace along with other secrets

```
root@master:~# kubectl -n kube-system get secret
NAME                                             TYPE                                  DATA   AGE
attachdetach-controller-token-9896p              kubernetes.io/service-account-token   3      14d
bizflycloud                                      Opaque                                2      10s
```

- Create RBAC role and role binding

```
kubectl apply -f manifest/cloud-controller-manager-roles.yaml
kubectl apply -f manifest/cloud-controller-manager-role-bindings.yaml
```

- Finally, run this command from the root of this repo to deploy BizFly Cloud Controller Manager as DaemonSet

```
kubectl apply -f manifest/bizfly-cloud-controller-manager-ds.yaml
```