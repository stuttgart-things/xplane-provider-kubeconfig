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

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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

func TestDetectClusterType(t *testing.T) {
	cases := map[string]struct {
		serverVersion string
		nodes         []corev1.Node
		want          string
	}{
		"K3sVersion": {
			serverVersion: "v1.31.4+k3s1",
			want:          "k3s",
		},
		"RKE2Version": {
			serverVersion: "v1.31.4+rke2r1",
			want:          "rke2",
		},
		"KindCluster": {
			serverVersion: "v1.35.0",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "my-cluster-control-plane"},
					Spec:       corev1.NodeSpec{ProviderID: ""},
				},
			},
			want: "kind",
		},
		"KindWorkerNode": {
			serverVersion: "v1.35.0",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "test-worker"},
					Spec:       corev1.NodeSpec{ProviderID: ""},
				},
			},
			want: "kind",
		},
		"KindWithProviderID": {
			serverVersion: "v1.35.0",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "dev-control-plane"},
					Spec:       corev1.NodeSpec{ProviderID: "kind://docker/dev/dev-control-plane"},
				},
			},
			want: "kind",
		},
		"CloudNodeNotKind": {
			serverVersion: "v1.35.0",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pool-control-plane"},
					Spec:       corev1.NodeSpec{ProviderID: "aws://us-east-1/i-abc123"},
				},
			},
			want: "k8s",
		},
		"GenericK8s": {
			serverVersion: "v1.35.0",
			nodes: []corev1.Node{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
				},
			},
			want: "k8s",
		},
		"NoNodes": {
			serverVersion: "v1.35.0",
			want:          "k8s",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			objs := make([]corev1.Node, len(tc.nodes))
			copy(objs, tc.nodes)

			cs := fake.NewSimpleClientset()
			for i := range objs {
				_, _ = cs.CoreV1().Nodes().Create(context.Background(), &objs[i], metav1.CreateOptions{})
			}

			got := detectClusterType(tc.serverVersion, context.Background(), cs)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("-want, +got:\n%s", diff)
			}
		})
	}
}
