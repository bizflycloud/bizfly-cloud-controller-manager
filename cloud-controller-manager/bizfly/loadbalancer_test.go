package bizfly

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "k8s.io/api/core/v1"

	"github.com/bizflycloud/gobizfly"
)

func TestBatchUpdateMembers_NoNodesKeepsMembers(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := newFakeBizflyClient(t, mux, ts.URL)

	mux.HandleFunc("/api/loadbalancers/loadbalancer/lb-1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "lb-1", "provisioning_status": "ACTIVE"})
	})
	var sent gobizfly.CloudLoadBalancerBatchMemberUpdateRequest
	mux.HandleFunc("/api/loadbalancers/pool/pool-1/member", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"members": []map[string]interface{}{
				{"id": "m-1", "name": "member_0_node-a", "address": "10.20.1.35", "protocol_port": 30080, "subnet_id": "sub-1"},
				{"id": "m-2", "name": "member_0_node-b", "address": "10.20.1.36", "protocol_port": 30080, "subnet_id": "sub-1"},
			}})
		case http.MethodPut:
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Errorf("decode batch update: %v", err)
			}
			w.WriteHeader(http.StatusAccepted)
		}
	})

	// No target node (e.g. VPN down) and an empty ServicePort, as the obsolete-listener path passes
	_, err := batchUpdateMembers(context.Background(), client, "pool-1", "lb-1", []*v1.Node{}, &v1.ServicePort{}, 0, "lb", "test", "")
	if err != nil {
		t.Fatalf("batchUpdateMembers returned error: %v", err)
	}
	if len(sent.Members) != 2 {
		t.Fatalf("expected both members kept, got %+v", sent.Members)
	}
	for _, m := range sent.Members {
		if m.ProtocolPort != 30080 || m.SubnetID != "sub-1" {
			t.Errorf("member %s changed: port=%d subnet=%s", m.Address, m.ProtocolPort, m.SubnetID)
		}
	}
}
