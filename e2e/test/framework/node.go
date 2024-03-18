package framework

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (i *Invocation) GetNodeList() ([]string, error) {
	workers := make([]string, 0)
	nodes, err := i.kubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	for _, node := range nodes.Items {
		workers = append(workers, node.Name)
	}
	return workers, nil
}
