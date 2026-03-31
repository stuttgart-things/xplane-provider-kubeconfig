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

// Package rbac bootstraps RBAC resources for downstream ProviderConfig access.
package rbac

import (
	"context"
	"os"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	clusterRoleName = "provider-kubeconfig-downstream"
	managedByLabel  = "app.kubernetes.io/managed-by"
	managedByValue  = "provider-kubeconfig"
)

// downstreamRules returns the RBAC rules for managing downstream ProviderConfigs.
func downstreamRules() []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{
		{
			APIGroups: []string{"kubernetes.crossplane.io"},
			Resources: []string{"providerconfigs"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"helm.crossplane.io"},
			Resources: []string{"providerconfigs"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"kubernetes.m.crossplane.io"},
			Resources: []string{"providerconfigs", "clusterproviderconfigs"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
		{
			APIGroups: []string{"helm.m.crossplane.io"},
			Resources: []string{"providerconfigs", "clusterproviderconfigs"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		},
	}
}

// EnsureDownstreamRBAC creates or updates the ClusterRole and ClusterRoleBinding
// for managing downstream ProviderConfigs. It detects the running service account
// from the pod and binds to it automatically.
func EnsureDownstreamRBAC(ctx context.Context, c client.Client) error {
	if err := ensureClusterRole(ctx, c); err != nil {
		return err
	}

	saName, ns := detectServiceAccount(ctx, c)
	if saName == "" {
		return nil
	}

	return ensureClusterRoleBinding(ctx, c, saName, ns)
}

func ensureClusterRole(ctx context.Context, c client.Client) error {
	cr := &rbacv1.ClusterRole{}
	err := c.Get(ctx, types.NamespacedName{Name: clusterRoleName}, cr)
	if kerrors.IsNotFound(err) {
		return c.Create(ctx, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:   clusterRoleName,
				Labels: map[string]string{managedByLabel: managedByValue},
			},
			Rules: downstreamRules(),
		})
	}
	if err != nil {
		return err
	}
	cr.Rules = downstreamRules()
	return c.Update(ctx, cr)
}

func ensureClusterRoleBinding(ctx context.Context, c client.Client, saName, ns string) error {
	crb := &rbacv1.ClusterRoleBinding{}
	err := c.Get(ctx, types.NamespacedName{Name: clusterRoleName}, crb)
	if kerrors.IsNotFound(err) {
		return c.Create(ctx, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:   clusterRoleName,
				Labels: map[string]string{managedByLabel: managedByValue},
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     clusterRoleName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: ns,
			}},
		})
	}
	if err != nil {
		return err
	}

	// Replace all subjects with the current SA. Only one provider revision
	// is active at a time, so stale SAs from previous upgrades are removed.
	desired := []rbacv1.Subject{{
		Kind:      "ServiceAccount",
		Name:      saName,
		Namespace: ns,
	}}
	if len(crb.Subjects) == 1 && crb.Subjects[0] == desired[0] {
		return nil // already correct
	}
	crb.Subjects = desired
	return c.Update(ctx, crb)
}

// detectServiceAccount discovers the service account by looking up our own pod.
func detectServiceAccount(ctx context.Context, c client.Client) (string, string) {
	podName := os.Getenv("HOSTNAME")
	ns := os.Getenv("POD_NAMESPACE")
	if ns == "" {
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			ns = string(data)
		}
	}
	if podName == "" || ns == "" {
		return "", ""
	}

	pod := &corev1.Pod{}
	if err := c.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns}, pod); err != nil {
		return "", ""
	}
	return pod.Spec.ServiceAccountName, ns
}
