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

package cluster

import (
	"context"
	"testing"
)

func TestGatherInvalidKubeconfig(t *testing.T) {
	_, err := Gather(context.Background(), []byte("not-a-kubeconfig"))
	if err == nil {
		t.Fatal("expected error for invalid kubeconfig, got nil")
	}
}

func TestGatherEmptyKubeconfig(t *testing.T) {
	_, err := Gather(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil kubeconfig, got nil")
	}
}

func TestGatherUnreachableCluster(t *testing.T) {
	// Valid kubeconfig format but unreachable server — should fail on server version
	kc := []byte(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:1
  name: test
contexts:
- context:
    cluster: test
    user: test
  name: test
current-context: test
users:
- name: test
  user:
    token: fake
`)
	_, err := Gather(context.Background(), kc)
	if err == nil {
		t.Fatal("expected error for unreachable cluster, got nil")
	}
}
