/*
Copyright 2025 The Crossplane Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vault

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/hashicorp/vault/api"
)

func TestReadKVv2(t *testing.T) {
	// Simulate Vault KVv2 GET /v1/secret/data/clusters/dev
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/secret/data/clusters/dev":
			resp := map[string]interface{}{
				"data": map[string]interface{}{
					"data": map[string]interface{}{
						"kubeconfig": "apiVersion: v1\nkind: Config\n",
					},
					"metadata": map[string]interface{}{
						"version": json.Number("3"),
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Use a no-op auth function since we control the server
	noAuth := func(c interface{}) error { return nil }
	_ = noAuth

	c, err := New(context.Background(), srv.URL, "", "secret", func(c *api.Client) error {
		c.SetToken("test")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	data, version, err := c.ReadKVv2(context.Background(), "clusters/dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "apiVersion: v1\nkind: Config\n"
	got, ok := data["kubeconfig"].(string)
	if !ok {
		t.Fatalf("kubeconfig key not a string, got %T", data["kubeconfig"])
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("data mismatch (-want +got):\n%s", diff)
	}
	if version != 3 {
		t.Errorf("version: want 3, got %d", version)
	}
}

func TestReadKVv2NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c, err := New(context.Background(), srv.URL, "", "secret", func(c *api.Client) error {
		c.SetToken("test")
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}

	_, _, err = c.ReadKVv2(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent path, got nil")
	}
}

func TestAuthAppRole(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/auth/approle/login" && r.Method == "PUT" {
			resp := map[string]interface{}{
				"auth": map[string]interface{}{
					"client_token": "s.test-token",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	authFn := AuthAppRole("test-role-id", "test-secret-id", "approle")
	_, err := New(context.Background(), srv.URL, "", "secret", authFn)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
