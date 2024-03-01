package framework

import (
	"context"
	"errors"
	"fmt"
	"github.com/bizflycloud/gobizfly"
)

var (
	ErrNotFound = errors.New("failed to find object")
)

func (f *Framework) GetBizflyClient() gobizfly.Client {
	return f.bizflyClient
}

func (f *Framework) GetLBByName(ctx context.Context, clusterName string, serviceName string) (string, error) {
	name := cutString(fmt.Sprintf("kube_service_%s_%s_%s", clusterName, f.Namespace(), serviceName))
	loadbalancers, err := f.GetBizflyClient().LoadBalancer.List(ctx, &gobizfly.ListOptions{})
	if err != nil {
		return "", err
	}
	for _, lb := range loadbalancers {
		if lb.Name == name {
			return lb.ID, nil
		}
	}
	return "", ErrNotFound
}

func (f *Framework) GetLB(ctx context.Context, clusterName string, serviceName string) (*gobizfly.LoadBalancer, error) {
	name := cutString(fmt.Sprintf("kube_service_%s_%s_%s", clusterName, f.Namespace(), serviceName))
	loadbalancers, err := f.GetBizflyClient().LoadBalancer.List(ctx, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, lb := range loadbalancers {
		if lb.Name == name {
			return lb, nil
		}
	}
	return nil, ErrNotFound
}

func (f *Framework) GetListners(ctx context.Context, lbId string) ([]*gobizfly.Listener, error) {
	listeners, err := f.GetBizflyClient().Listener.List(ctx, lbId, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	return listeners, nil
}

func (f *Framework) GetPools(ctx context.Context, lbId string) ([]*gobizfly.Pool, error) {
	pools, err := f.GetBizflyClient().Pool.List(ctx, lbId, &gobizfly.ListOptions{})
	if err != nil {
		return nil, err
	}
	return pools, nil
}

func (f *Framework) GetMembersByPools(ctx context.Context, pools []*gobizfly.Pool) (int, error) {
	var members int
	for _, pool := range pools {
		listMembers, err := f.GetBizflyClient().Member.List(ctx, pool.ID, &gobizfly.ListOptions{})
		if err != nil {
			return 0, err
		}
		members += len(listMembers)
	}
	return members, nil
}
