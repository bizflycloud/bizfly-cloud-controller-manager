package bizfly

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
