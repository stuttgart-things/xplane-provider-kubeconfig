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

package remotecluster

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	xpv2 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource/fake"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"

	v1alpha1 "github.com/stuttgart-things/provider-kubeconfig/apis/kubeconfig/v1alpha1"
)

// --- helpers ---

func newRemoteCluster(name, ns, path string) *v1alpha1.RemoteCluster {
	return &v1alpha1.RemoteCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.RemoteClusterSpec{
			ForProvider: v1alpha1.RemoteClusterParameters{
				Source:          v1alpha1.RemoteClusterSource{Path: path},
				SecretNamespace: ns,
			},
		},
	}
}

func newRemoteClusterWithProviderConfigs(name, ns, path string, pcs []v1alpha1.ProviderConfigRef) *v1alpha1.RemoteCluster {
	cr := newRemoteCluster(name, ns, path)
	cr.Spec.ForProvider.ProviderConfigs = pcs
	return cr
}

// --- mock client ---

type mockClient struct {
	client.Client
	MockGet    func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
	MockCreate func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	MockUpdate func(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error
	MockDelete func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
	MockList   func(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

func (m *mockClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	if m.MockGet != nil {
		return m.MockGet(ctx, key, obj, opts...)
	}
	return nil
}

func (m *mockClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if m.MockCreate != nil {
		return m.MockCreate(ctx, obj, opts...)
	}
	return nil
}

func (m *mockClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if m.MockUpdate != nil {
		return m.MockUpdate(ctx, obj, opts...)
	}
	return nil
}

func (m *mockClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	if m.MockDelete != nil {
		return m.MockDelete(ctx, obj, opts...)
	}
	return nil
}

func (m *mockClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if m.MockList != nil {
		return m.MockList(ctx, list, opts...)
	}
	return nil
}

// --- unit tests for helpers ---

func TestSecretName(t *testing.T) {
	if got := secretName("dev-cluster"); got != "kubeconfig-dev-cluster" {
		t.Errorf("secretName: want %q, got %q", "kubeconfig-dev-cluster", got)
	}
}

func TestContentHash(t *testing.T) {
	data := []byte("hello world")
	h1 := contentHash(data)
	h2 := contentHash(data)
	if h1 != h2 {
		t.Error("same data should produce same hash")
	}
	h3 := contentHash([]byte("different"))
	if h1 == h3 {
		t.Error("different data should produce different hash")
	}
	if len(h1) != 64 {
		t.Errorf("sha256 hex should be 64 chars, got %d", len(h1))
	}
}

func TestBuildDownstreamProviderConfig(t *testing.T) {
	cases := map[string]struct {
		pcRef     v1alpha1.ProviderConfigRef
		wantGVK   schema.GroupVersionKind
		wantErr   bool
	}{
		"ProviderKubernetes": {
			pcRef:   v1alpha1.ProviderConfigRef{Name: "my-k8s", Type: "provider-kubernetes"},
			wantGVK: schema.GroupVersionKind{Group: "kubernetes.crossplane.io", Version: "v1alpha1", Kind: "ProviderConfig"},
		},
		"ProviderHelm": {
			pcRef:   v1alpha1.ProviderConfigRef{Name: "my-helm", Type: "provider-helm"},
			wantGVK: schema.GroupVersionKind{Group: "helm.crossplane.io", Version: "v1beta1", Kind: "ProviderConfig"},
		},
		"UnsupportedType": {
			pcRef:   v1alpha1.ProviderConfigRef{Name: "bad", Type: "provider-unknown"},
			wantErr: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			u, err := buildDownstreamProviderConfig(tc.pcRef, "kubeconfig-dev", "crossplane-system", "dev")
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if diff := cmp.Diff(tc.wantGVK, u.GroupVersionKind()); diff != "" {
				t.Errorf("GVK: -want, +got:\n%s", diff)
			}
			if u.GetName() != tc.pcRef.Name {
				t.Errorf("name: want %q, got %q", tc.pcRef.Name, u.GetName())
			}
			if u.GetLabels()[labelRemoteCluster] != "dev" {
				t.Errorf("label %s: want %q, got %q", labelRemoteCluster, "dev", u.GetLabels()[labelRemoteCluster])
			}

			creds, _, _ := unstructured.NestedMap(u.Object, "spec", "credentials")
			if creds["source"] != "Secret" {
				t.Errorf("credentials.source: want %q, got %v", "Secret", creds["source"])
			}
			secretRef, _ := creds["secretRef"].(map[string]interface{})
			if secretRef["name"] != "kubeconfig-dev" {
				t.Errorf("secretRef.name: want %q, got %v", "kubeconfig-dev", secretRef["name"])
			}
			if secretRef["namespace"] != "crossplane-system" {
				t.Errorf("secretRef.namespace: want %q, got %v", "crossplane-system", secretRef["namespace"])
			}
			if secretRef["key"] != "kubeconfig" {
				t.Errorf("secretRef.key: want %q, got %v", "kubeconfig", secretRef["key"])
			}
		})
	}
}

// --- Observe tests ---

func TestObserveNotRemoteCluster(t *testing.T) {
	e := &external{kube: &mockClient{}}
	_, err := e.Observe(context.Background(), &fake.Managed{})
	if diff := cmp.Diff(errors.New(errNotRemoteCluster), err, test.EquateErrors()); diff != "" {
		t.Errorf("-want error, +got error:\n%s", diff)
	}
}

func TestObserveSecretNotFound(t *testing.T) {
	mc := &mockClient{
		MockGet: func(_ context.Context, key types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
			return kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
		},
	}

	cr := newRemoteCluster("dev", "default", "clusters/dev.yaml")
	e := &external{kube: mc}
	got, err := e.Observe(context.Background(), cr)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ResourceExists {
		t.Error("expected ResourceExists=false")
	}
}

func TestObserveDefaultNamespace(t *testing.T) {
	var capturedKey types.NamespacedName
	mc := &mockClient{
		MockGet: func(_ context.Context, key types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
			capturedKey = key
			return kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
		},
	}

	cr := newRemoteCluster("dev", "", "clusters/dev.yaml")
	e := &external{kube: mc}
	_, _ = e.Observe(context.Background(), cr)

	if capturedKey.Namespace != "crossplane-system" {
		t.Errorf("expected default namespace %q, got %q", "crossplane-system", capturedKey.Namespace)
	}
}

// --- Delete tests ---

func TestDeleteNotRemoteCluster(t *testing.T) {
	e := &external{kube: &mockClient{}}
	_, err := e.Delete(context.Background(), &fake.Managed{})
	if diff := cmp.Diff(errors.New(errNotRemoteCluster), err, test.EquateErrors()); diff != "" {
		t.Errorf("-want error, +got error:\n%s", diff)
	}
}

func TestDeleteSuccess(t *testing.T) {
	mc := &mockClient{
		MockDelete: func(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
			return nil
		},
		MockList: func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			return nil
		},
	}

	cr := newRemoteCluster("dev", "default", "clusters/dev.yaml")
	e := &external{kube: mc}
	_, err := e.Delete(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteSecretAlreadyGone(t *testing.T) {
	mc := &mockClient{
		MockDelete: func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
			return kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, obj.GetName())
		},
		MockList: func(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
			return nil
		},
	}

	cr := newRemoteCluster("dev", "default", "clusters/dev.yaml")
	e := &external{kube: mc}
	_, err := e.Delete(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteCleansUpDownstreamProviderConfigs(t *testing.T) {
	var deletedNames []string
	mc := &mockClient{
		MockDelete: func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
			deletedNames = append(deletedNames, obj.GetName())
			return nil
		},
		MockList: func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
			ul := list.(*unstructured.UnstructuredList)
			item := unstructured.Unstructured{}
			item.SetName("stale-pc")
			ul.Items = []unstructured.Unstructured{item}
			return nil
		},
	}

	cr := newRemoteCluster("dev", "default", "clusters/dev.yaml")
	e := &external{kube: mc}
	_, err := e.Delete(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have deleted the Secret + stale ProviderConfigs from each GVK type
	found := false
	for _, n := range deletedNames {
		if n == "stale-pc" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected stale-pc to be deleted, deleted: %v", deletedNames)
	}
}

// --- Update tests ---

func TestUpdateNotRemoteCluster(t *testing.T) {
	e := &external{kube: &mockClient{}}
	_, err := e.Update(context.Background(), &fake.Managed{})
	if diff := cmp.Diff(errors.New(errNotRemoteCluster), err, test.EquateErrors()); diff != "" {
		t.Errorf("-want error, +got error:\n%s", diff)
	}
}

// --- Create tests ---

func TestCreateNotRemoteCluster(t *testing.T) {
	e := &external{kube: &mockClient{}}
	_, err := e.Create(context.Background(), &fake.Managed{})
	if diff := cmp.Diff(errors.New(errNotRemoteCluster), err, test.EquateErrors()); diff != "" {
		t.Errorf("-want error, +got error:\n%s", diff)
	}
}

// --- downstreamProviderConfigsUpToDate tests ---

func TestDownstreamProviderConfigsUpToDate(t *testing.T) {
	cases := map[string]struct {
		pcs    []v1alpha1.ProviderConfigRef
		getErr error
		want   bool
	}{
		"AllExist": {
			pcs: []v1alpha1.ProviderConfigRef{
				{Name: "my-k8s", Type: "provider-kubernetes"},
			},
			getErr: nil,
			want:   true,
		},
		"MissingOne": {
			pcs: []v1alpha1.ProviderConfigRef{
				{Name: "my-k8s", Type: "provider-kubernetes"},
			},
			getErr: kerrors.NewNotFound(schema.GroupResource{}, "my-k8s"),
			want:   false,
		},
		"UnsupportedType": {
			pcs: []v1alpha1.ProviderConfigRef{
				{Name: "bad", Type: "provider-unknown"},
			},
			want: false,
		},
		"NoneDesired": {
			pcs:  nil,
			want: true,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mc := &mockClient{
				MockGet: func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
					if tc.getErr != nil {
						return tc.getErr
					}
					return nil
				},
			}
			cr := newRemoteClusterWithProviderConfigs("dev", "default", "clusters/dev.yaml", tc.pcs)
			e := &external{kube: mc}
			got := e.downstreamProviderConfigsUpToDate(context.Background(), cr)
			if got != tc.want {
				t.Errorf("want %v, got %v", tc.want, got)
			}
		})
	}
}

// --- ensureDownstreamProviderConfigs tests ---

func TestEnsureDownstreamProviderConfigs(t *testing.T) {
	t.Run("CreatesMissing", func(t *testing.T) {
		var created []string
		mc := &mockClient{
			MockGet: func(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
				// Check if it's an unstructured (downstream PC) or a Secret
				if _, ok := obj.(*unstructured.Unstructured); ok {
					return kerrors.NewNotFound(schema.GroupResource{}, key.Name)
				}
				return nil
			},
			MockCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
				created = append(created, obj.GetName())
				return nil
			},
			MockList: func(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
				return nil
			},
		}

		cr := newRemoteClusterWithProviderConfigs("dev", "default", "clusters/dev.yaml", []v1alpha1.ProviderConfigRef{
			{Name: "my-k8s", Type: "provider-kubernetes"},
			{Name: "my-helm", Type: "provider-helm"},
		})
		e := &external{kube: mc}
		err := e.ensureDownstreamProviderConfigs(context.Background(), cr, "kubeconfig-dev", "crossplane-system")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(created) != 2 {
			t.Errorf("expected 2 creates, got %d: %v", len(created), created)
		}
	})

	t.Run("SkipsExisting", func(t *testing.T) {
		var created []string
		mc := &mockClient{
			MockGet: func(_ context.Context, _ types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
				return nil // already exists
			},
			MockCreate: func(_ context.Context, obj client.Object, _ ...client.CreateOption) error {
				created = append(created, obj.GetName())
				return nil
			},
			MockList: func(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
				return nil
			},
		}

		cr := newRemoteClusterWithProviderConfigs("dev", "default", "clusters/dev.yaml", []v1alpha1.ProviderConfigRef{
			{Name: "my-k8s", Type: "provider-kubernetes"},
		})
		e := &external{kube: mc}
		err := e.ensureDownstreamProviderConfigs(context.Background(), cr, "kubeconfig-dev", "crossplane-system")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(created) != 0 {
			t.Errorf("expected 0 creates for existing, got %d: %v", len(created), created)
		}
	})

	t.Run("DeletesStale", func(t *testing.T) {
		var deleted []string
		mc := &mockClient{
			MockGet: func(_ context.Context, _ types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
				return nil
			},
			MockList: func(_ context.Context, list client.ObjectList, _ ...client.ListOption) error {
				ul := list.(*unstructured.UnstructuredList)
				stale := unstructured.Unstructured{}
				stale.SetName("stale-old-pc")
				ul.Items = []unstructured.Unstructured{stale}
				return nil
			},
			MockDelete: func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
				deleted = append(deleted, obj.GetName())
				return nil
			},
		}

		// Desired has "my-k8s" but list returns "stale-old-pc"
		cr := newRemoteClusterWithProviderConfigs("dev", "default", "clusters/dev.yaml", []v1alpha1.ProviderConfigRef{
			{Name: "my-k8s", Type: "provider-kubernetes"},
		})
		e := &external{kube: mc}
		err := e.ensureDownstreamProviderConfigs(context.Background(), cr, "kubeconfig-dev", "crossplane-system")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		found := false
		for _, n := range deleted {
			if n == "stale-old-pc" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected stale-old-pc to be deleted, deleted: %v", deleted)
		}
	})
}

// --- ObserveSetsAvailableCondition (requires mock that doesn't trigger cloneAndReadFile) ---
// Note: Full Observe integration with git is tested via e2e. Here we test the
// condition-setting path by checking the Observe path where Secret is not found
// (which returns early before cloneAndReadFile).

func TestObserveSetsAvailableConditionRequiresSecret(t *testing.T) {
	// When Secret is not found, Available should NOT be set
	mc := &mockClient{
		MockGet: func(_ context.Context, key types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
			return kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
		},
	}

	cr := newRemoteCluster("dev", "default", "clusters/dev.yaml")
	e := &external{kube: mc}
	obs, err := e.Observe(context.Background(), cr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.ResourceExists {
		t.Error("expected ResourceExists=false")
	}
	// Ready condition should not be set when resource doesn't exist
	cond := cr.GetCondition(xpv2.TypeReady)
	if cond.Status == corev1.ConditionTrue {
		t.Error("Ready should not be True when Secret doesn't exist")
	}
}

// Silence unused import warnings - these are used by mock methods
var _ = runtime.Object(nil)
