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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	xpv2 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource/fake"
	"github.com/crossplane/crossplane-runtime/v2/pkg/test"

	v1alpha1 "github.com/stuttgart-things/provider-kubeconfig/apis/kubeconfig/v1alpha1"
)

func TestSecretName(t *testing.T) {
	cases := map[string]struct {
		crName string
		want   string
	}{
		"Simple": {
			crName: "dev-cluster",
			want:   "kubeconfig-dev-cluster",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := secretName(tc.crName)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("secretName(%q): -want, +got:\n%s", tc.crName, diff)
			}
		})
	}
}

type mockClient struct {
	client.Client
	MockGet    func(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error
	MockCreate func(ctx context.Context, obj client.Object, opts ...client.CreateOption) error
	MockDelete func(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error
}

func (m *mockClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	return m.MockGet(ctx, key, obj, opts...)
}

func (m *mockClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	return m.MockCreate(ctx, obj, opts...)
}

func (m *mockClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	return m.MockDelete(ctx, obj, opts...)
}

func newRemoteCluster(name, ns, path string) *v1alpha1.RemoteCluster {
	cr := &v1alpha1.RemoteCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.RemoteClusterSpec{
			ForProvider: v1alpha1.RemoteClusterParameters{
				Source:          v1alpha1.RemoteClusterSource{Path: path},
				SecretNamespace: ns,
			},
		},
	}
	return cr
}

func TestObserve(t *testing.T) {
	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		o   managed.ExternalObservation
		err error
	}

	cases := map[string]struct {
		reason string
		client *mockClient
		args   args
		want   want
	}{
		"NotRemoteCluster": {
			reason: "Should return error if managed resource is not a RemoteCluster",
			client: &mockClient{},
			args: args{
				ctx: context.Background(),
				mg:  &fake.Managed{},
			},
			want: want{
				err: errors.New(errNotRemoteCluster),
			},
		},
		"SecretNotFound": {
			reason: "Should return ResourceExists=false when Secret does not exist",
			client: &mockClient{
				MockGet: func(_ context.Context, key types.NamespacedName, _ client.Object, _ ...client.GetOption) error {
					return kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
				},
			},
			args: args{
				ctx: context.Background(),
				mg:  newRemoteCluster("dev", "default", "clusters/dev.yaml"),
			},
			want: want{
				o: managed.ExternalObservation{ResourceExists: false},
			},
		},
		"SecretExists": {
			reason: "Should return ResourceExists=true with kubeconfig connection details",
			client: &mockClient{
				MockGet: func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
					s := obj.(*corev1.Secret)
					s.Data = map[string][]byte{"kubeconfig": []byte("kube-data")}
					return nil
				},
			},
			args: args{
				ctx: context.Background(),
				mg:  newRemoteCluster("dev", "default", "clusters/dev.yaml"),
			},
			want: want{
				o: managed.ExternalObservation{
					ResourceExists:   true,
					ResourceUpToDate: true,
					ConnectionDetails: managed.ConnectionDetails{
						"kubeconfig": []byte("kube-data"),
					},
				},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{kube: tc.client}
			got, err := e.Observe(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.o, got); diff != "" {
				t.Errorf("\n%s\ne.Observe(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	type args struct {
		ctx context.Context
		mg  resource.Managed
	}

	type want struct {
		o   managed.ExternalDelete
		err error
	}

	cases := map[string]struct {
		reason string
		client *mockClient
		args   args
		want   want
	}{
		"NotRemoteCluster": {
			reason: "Should return error if managed resource is not a RemoteCluster",
			client: &mockClient{},
			args: args{
				ctx: context.Background(),
				mg:  &fake.Managed{},
			},
			want: want{
				err: errors.New(errNotRemoteCluster),
			},
		},
		"SuccessfulDelete": {
			reason: "Should delete the kubeconfig Secret successfully",
			client: &mockClient{
				MockDelete: func(_ context.Context, _ client.Object, _ ...client.DeleteOption) error {
					return nil
				},
			},
			args: args{
				ctx: context.Background(),
				mg:  newRemoteCluster("dev", "default", "clusters/dev.yaml"),
			},
			want: want{
				o: managed.ExternalDelete{},
			},
		},
		"SecretAlreadyGone": {
			reason: "Should not error if Secret is already deleted",
			client: &mockClient{
				MockDelete: func(_ context.Context, obj client.Object, _ ...client.DeleteOption) error {
					return kerrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, obj.GetName())
				},
			},
			args: args{
				ctx: context.Background(),
				mg:  newRemoteCluster("dev", "default", "clusters/dev.yaml"),
			},
			want: want{
				o: managed.ExternalDelete{},
			},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &external{kube: tc.client}
			got, err := e.Delete(tc.args.ctx, tc.args.mg)
			if diff := cmp.Diff(tc.want.err, err, test.EquateErrors()); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want error, +got error:\n%s\n", tc.reason, diff)
			}
			if diff := cmp.Diff(tc.want.o, got); diff != "" {
				t.Errorf("\n%s\ne.Delete(...): -want, +got:\n%s\n", tc.reason, diff)
			}
		})
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

func TestObserveSetsAvailableCondition(t *testing.T) {
	mc := &mockClient{
		MockGet: func(_ context.Context, _ types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
			s := obj.(*corev1.Secret)
			s.Data = map[string][]byte{"kubeconfig": []byte("data")}
			return nil
		},
	}

	cr := newRemoteCluster("dev", "default", "clusters/dev.yaml")
	e := &external{kube: mc}
	_, _ = e.Observe(context.Background(), cr)

	cond := cr.GetCondition(xpv2.TypeReady)
	if cond.Status != corev1.ConditionTrue {
		t.Errorf("expected Ready=True, got %v", cond.Status)
	}
	if cond.Reason != xpv2.ReasonAvailable {
		t.Errorf("expected reason %q, got %q", xpv2.ReasonAvailable, cond.Reason)
	}
}

