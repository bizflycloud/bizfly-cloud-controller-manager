package bizfly

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/bizflycloud/gobizfly"
)

func TestServerByID_NotFoundWhenEverywhereFails(t *testing.T) {
	mux := http.NewServeMux()
	serverTest := httptest.NewServer(mux)
	defer serverTest.Close()

	testRegion := "HN"

	// Mock /api/token
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "fake-keystone-token",
			"project_id": "fake-project-id",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	})

	// Mock /api/auth/service
	mux.HandleFunc("/api/auth/service", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"services": []map[string]interface{}{
				{
					"name":           "Cloud Server",
					"canonical_name": "cloud_server",
					"service_url":    serverTest.URL + "/iaas-cloud/api",
					"region":         "HaNoi",
				},
				{
					"name":           "Kubernetes Engine",
					"canonical_name": "kubernetes-engine",
					"service_url":    serverTest.URL + "/api/kubernetes-engine",
					"region":         "HaNoi",
				},
			},
		})
	})

	// 1. deleted-node-123: CloudServer returns 404 Not Found
	mux.HandleFunc("/iaas-cloud/api/servers/deleted-node-123", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "Server not found"}`))
	})

	// BKE Everywhere endpoint returns 400 Bad Request (Everywhere is no longer supported)
	mux.HandleFunc("/api/kubernetes-engine/_/node_everywhere/deleted-node-123", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": "EVERYWHERE_NOT_SUPPORTED", "message": "Everywhere is no longer supported"}`))
	})

	// Server list used to confirm a NotFound: deleted-node-123 is not in it
	mux.HandleFunc("/iaas-cloud/api/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{"id": "running-node-456", "name": "running-node", "status": "ACTIVE"},
			{"id": "shutoff-node-789", "name": "shutoff-node", "status": "SHUTOFF"},
		})
	})

	// 2. running-node-456: Active running server
	mux.HandleFunc("/iaas-cloud/api/servers/running-node-456", func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Received request for running-node-456: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "running-node-456",
			"name":   "running-node",
			"status": "ACTIVE",
		})
	})

	// 3. shutoff-node-789: Shutoff server
	mux.HandleFunc("/iaas-cloud/api/servers/shutoff-node-789", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "shutoff-node-789",
			"name":   "shutoff-node",
			"status": "SHUTOFF",
		})
	})

	client, err := gobizfly.NewClient(gobizfly.WithAPIURL(serverTest.URL), gobizfly.WithRegionName(testRegion))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	tok, err := client.Token.Init(ctx, &gobizfly.TokenCreateRequest{
		AuthMethod: "password",
		Username:   "test",
		Password:   "test",
		ProjectID:  "fake-project-id",
	})
	if err != nil {
		t.Fatalf("Failed to init token: %v", err)
	}
	client.SetKeystoneToken(tok)

	s := &servers{gclient: client}

	// Case 1: Deleted node -> serverByID returns ErrNotFound
	server, node, err := serverByID(ctx, client, "deleted-node-123")
	if server != nil || node != nil {
		t.Fatalf("Expected nil server and node, got server=%v, node=%v", server, node)
	}
	if !errors.Is(err, gobizfly.ErrNotFound) {
		t.Fatalf("Expected gobizfly.ErrNotFound, got: %v", err)
	}

	// Case 1b: InstanceExistsByProviderID -> (false, nil) so Kubernetes removes node
	exists, err := s.InstanceExistsByProviderID(ctx, "bizflycloud://deleted-node-123")
	if err != nil {
		t.Fatalf("Expected nil error so Kubernetes deletes the node, got: %v", err)
	}
	if exists {
		t.Fatalf("Expected exists=false for deleted node, got true")
	}

	// Case 1c: InstanceShutdownByProviderID -> (false, nil)
	shutdown, err := s.InstanceShutdownByProviderID(ctx, "bizflycloud://deleted-node-123")
	if err != nil {
		t.Fatalf("Expected nil error for deleted node, got: %v", err)
	}
	if shutdown {
		t.Fatalf("Expected shutdown=false for deleted node, got true")
	}

	// Case 2: Running active node
	exists, err = s.InstanceExistsByProviderID(ctx, "bizflycloud://running-node-456")
	if err != nil || !exists {
		t.Fatalf("Expected exists=true for running node, got: %v, err: %v", exists, err)
	}

	shutdown, err = s.InstanceShutdownByProviderID(ctx, "bizflycloud://running-node-456")
	if err != nil || shutdown {
		t.Fatalf("Expected shutdown=false for running node, got: %v, err: %v", shutdown, err)
	}

	// Case 3: Shutoff node
	exists, err = s.InstanceExistsByProviderID(ctx, "bizflycloud://shutoff-node-789")
	if err != nil || !exists {
		t.Fatalf("Expected exists=true for shutoff node, got: %v, err: %v", exists, err)
	}

	shutdown, err = s.InstanceShutdownByProviderID(ctx, "bizflycloud://shutoff-node-789")
	if err != nil || !shutdown {
		t.Fatalf("Expected shutdown=true for shutoff node, got: %v, err: %v", shutdown, err)
	}
}

func TestNodeAddresses_FloatingAndFixedIPs(t *testing.T) {
	// Simulate an Egress node in VPC with fixed private IP and floating public IP
	server := &gobizfly.Server{
		ID:   "egr1-node-id",
		Name: "k8s-dev2-egr1-dg36ke655dsg4rdp-node-mto4ihw3",
		Addresses: map[string]interface{}{
			"priv_vpc_network": []interface{}{
				map[string]interface{}{
					"OS-EXT-IPS:type":         "fixed",
					"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:aa:bb:cc",
					"version":                 4,
					"addr":                    "10.20.3.31",
				},
				map[string]interface{}{
					"OS-EXT-IPS:type":         "floating",
					"OS-EXT-IPS-MAC:mac_addr": "fa:16:3e:aa:bb:cc",
					"version":                 4,
					"addr":                    "103.56.158.103",
				},
			},
		},
	}

	addrs, err := nodeAdddresses(server, nil)
	if err != nil {
		t.Fatalf("nodeAdddresses returned error: %v", err)
	}

	var hasInternal, hasExternal bool
	for _, a := range addrs {
		if a.Type == v1.NodeInternalIP && a.Address == "10.20.3.31" {
			hasInternal = true
		}
		if a.Type == v1.NodeExternalIP && a.Address == "103.56.158.103" {
			hasExternal = true
		}
	}

	if !hasInternal {
		t.Errorf("Expected NodeInternalIP 10.20.3.31, got: %v", addrs)
	}
	if !hasExternal {
		t.Errorf("Expected NodeExternalIP 103.56.158.103, got: %v", addrs)
	}
}

func TestNodeAddresses_WanV4AndWanV6(t *testing.T) {
	server := &gobizfly.Server{
		ID:   "pool2-node-id",
		Name: "k8s-dev2-pool2-node",
		IPAddresses: gobizfly.IPAddress{
			LanAddresses: []gobizfly.IP{
				{Address: "10.20.3.96"},
			},
			WanV4Addresses: []gobizfly.IP{
				{Address: "14.225.29.96"},
			},
			WanV6Addresses: []gobizfly.IP{
				{Address: "2400:8902::1"},
			},
		},
	}

	addrs, err := nodeAdddresses(server, nil)
	if err != nil {
		t.Fatalf("nodeAdddresses returned error: %v", err)
	}

	var hasInternal, hasWanV4, hasWanV6 bool
	for _, a := range addrs {
		if a.Type == v1.NodeInternalIP && a.Address == "10.20.3.96" {
			hasInternal = true
		}
		if a.Type == v1.NodeExternalIP && a.Address == "14.225.29.96" {
			hasWanV4 = true
		}
		if a.Type == v1.NodeExternalIP && a.Address == "2400:8902::1" {
			hasWanV6 = true
		}
	}

	if !hasInternal {
		t.Errorf("Expected NodeInternalIP 10.20.3.96, got: %v", addrs)
	}
	if !hasWanV4 {
		t.Errorf("Expected NodeExternalIP 14.225.29.96, got: %v", addrs)
	}
	if !hasWanV6 {
		t.Errorf("Expected NodeExternalIP 2400:8902::1, got: %v", addrs)
	}
}

func TestServerIDFromProviderID_Formats(t *testing.T) {
	testCases := []struct {
		name       string
		providerID string
		expectedID string
	}{
		{
			name:       "2-slash standard",
			providerID: "bizflycloud://c0f541d1-385a-4b0f-8c9a-5bd583475477",
			expectedID: "c0f541d1-385a-4b0f-8c9a-5bd583475477",
		},
		{
			name:       "3-slash standard k8s URI",
			providerID: "bizflycloud:///c0f541d1-385a-4b0f-8c9a-5bd583475477",
			expectedID: "c0f541d1-385a-4b0f-8c9a-5bd583475477",
		},
		{
			name:       "with region prefix",
			providerID: "bizflycloud://HN/c0f541d1-385a-4b0f-8c9a-5bd583475477",
			expectedID: "c0f541d1-385a-4b0f-8c9a-5bd583475477",
		},
		{
			name:       "bare UUID without scheme",
			providerID: "c0f541d1-385a-4b0f-8c9a-5bd583475477",
			expectedID: "c0f541d1-385a-4b0f-8c9a-5bd583475477",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := serverIDFromProviderID(tc.providerID)
			if err != nil {
				t.Fatalf("serverIDFromProviderID failed for %s: %v", tc.providerID, err)
			}
			if id != tc.expectedID {
				t.Fatalf("expected ID %s, got %s", tc.expectedID, id)
			}
		})
	}
}

func TestServerByName_FetchesFullDetails(t *testing.T) {
	mux := http.NewServeMux()
	serverTest := httptest.NewServer(mux)
	defer serverTest.Close()

	// Mock /api/token
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "fake-keystone-token",
			"project_id": "fake-project-id",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	})

	// Mock /api/auth/service
	mux.HandleFunc("/api/auth/service", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"services": []map[string]interface{}{
				{
					"name":           "Cloud Server",
					"canonical_name": "cloud_server",
					"service_url":    serverTest.URL + "/iaas-cloud/api",
					"region":         "HaNoi",
				},
			},
		})
	})

	// CloudServer.List returns minimal server summary without addresses
	mux.HandleFunc("/iaas-cloud/api/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{
			{
				"id":     "node-uuid-123",
				"name":   "my-test-node",
				"status": "ACTIVE",
			},
		})
	})

	// CloudServer.Get returns full details including addresses
	mux.HandleFunc("/iaas-cloud/api/servers/node-uuid-123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":     "node-uuid-123",
			"name":   "my-test-node",
			"status": "ACTIVE",
			"addresses": map[string]interface{}{
				"LAN": []map[string]interface{}{
					{"addr": "10.20.3.31", "OS-EXT-IPS:type": "fixed", "version": 4},
					{"addr": "103.56.158.103", "OS-EXT-IPS:type": "floating", "version": 4},
				},
			},
		})
	})

	client, err := gobizfly.NewClient(gobizfly.WithAPIURL(serverTest.URL), gobizfly.WithRegionName("HN"))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	ctx := context.Background()
	tok, err := client.Token.Init(ctx, &gobizfly.TokenCreateRequest{
		AuthMethod: "password",
		Username:   "test",
		Password:   "test",
		ProjectID:  "fake-project-id",
	})
	if err != nil {
		t.Fatalf("Failed to init token: %v", err)
	}
	client.SetKeystoneToken(tok)
	server, err := serverByName(ctx, client, types.NodeName("my-test-node"))
	if err != nil {
		t.Fatalf("serverByName failed: %v", err)
	}

	if server.ID != "node-uuid-123" {
		t.Fatalf("Expected server ID node-uuid-123, got: %s", server.ID)
	}

	if server.Addresses == nil {
		t.Fatalf("Expected full server details with Addresses populated, got nil")
	}

	addrs, err := nodeAdddresses(server, nil)
	if err != nil {
		t.Fatalf("nodeAdddresses failed: %v", err)
	}

	var hasExt bool
	for _, a := range addrs {
		if a.Type == v1.NodeExternalIP && a.Address == "103.56.158.103" {
			hasExt = true
		}
	}
	if !hasExt {
		t.Fatalf("Expected NodeExternalIP 103.56.158.103 from serverByName, got: %v", addrs)
	}
}

func newFakeBizflyClient(t *testing.T, mux *http.ServeMux, url string) *gobizfly.Client {
	t.Helper()
	mux.HandleFunc("/api/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "fake-keystone-token",
			"project_id": "fake-project-id",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	})
	mux.HandleFunc("/api/auth/service", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"services": []map[string]interface{}{
				{"name": "Cloud Server", "canonical_name": "cloud_server", "service_url": url + "/iaas-cloud/api", "region": "HaNoi"},
				{"name": "Kubernetes Engine", "canonical_name": "kubernetes-engine", "service_url": url + "/api/kubernetes-engine", "region": "HaNoi"},
			},
		})
	})
	client, err := gobizfly.NewClient(gobizfly.WithAPIURL(url), gobizfly.WithRegionName("HN"))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	tok, err := client.Token.Init(context.Background(), &gobizfly.TokenCreateRequest{
		AuthMethod: "password", Username: "test", Password: "test", ProjectID: "fake-project-id",
	})
	if err != nil {
		t.Fatalf("Failed to init token: %v", err)
	}
	client.SetKeystoneToken(tok)
	return client
}

func TestInstanceExistsByProviderID_ConfirmsNotFound(t *testing.T) {
	cases := []struct {
		name       string
		listStatus int
		listBody   string
		wantExists bool
		wantErr    bool
	}{
		{"transient 404 but still listed", http.StatusOK, `[{"id": "flaky-node", "status": "ACTIVE"}]`, true, false},
		{"list fails", http.StatusInternalServerError, `{"message": "boom"}`, false, true},
		{"empty list", http.StatusOK, `[]`, false, true},
		{"really gone", http.StatusOK, `[{"id": "other-node", "status": "ACTIVE"}]`, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			serverTest := httptest.NewServer(mux)
			defer serverTest.Close()
			client := newFakeBizflyClient(t, mux, serverTest.URL)

			mux.HandleFunc("/iaas-cloud/api/servers/flaky-node", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message": "Server not found"}`))
			})
			mux.HandleFunc("/api/kubernetes-engine/_/node_everywhere/flaky-node", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"code": "EVERYWHERE_NOT_SUPPORTED"}`))
			})
			mux.HandleFunc("/iaas-cloud/api/servers", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.listStatus)
				_, _ = w.Write([]byte(tc.listBody))
			})

			s := &servers{gclient: client}
			exists, err := s.InstanceExistsByProviderID(context.Background(), "bizflycloud://flaky-node")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if exists != tc.wantExists {
				t.Fatalf("exists = %v, want %v", exists, tc.wantExists)
			}
		})
	}
}
