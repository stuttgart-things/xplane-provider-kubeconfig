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
	"crypto/sha256"
	"fmt"

	xpv2 "github.com/crossplane/crossplane-runtime/v2/apis/common/v1"
	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/crossplane/crossplane-runtime/v2/pkg/controller"
	"github.com/crossplane/crossplane-runtime/v2/pkg/event"
	"github.com/crossplane/crossplane-runtime/v2/pkg/ratelimiter"
	"github.com/crossplane/crossplane-runtime/v2/pkg/reconciler/managed"
	"github.com/crossplane/crossplane-runtime/v2/pkg/resource"
	"github.com/crossplane/crossplane-runtime/v2/pkg/statemetrics"

	v1alpha1 "github.com/stuttgart-things/provider-kubeconfig/apis/kubeconfig/v1alpha1"
	apisv1alpha1 "github.com/stuttgart-things/provider-kubeconfig/apis/v1alpha1"
	clusterpkg "github.com/stuttgart-things/provider-kubeconfig/internal/cluster"
	decryptpkg "github.com/stuttgart-things/provider-kubeconfig/internal/decrypt"
	gitpkg "github.com/stuttgart-things/provider-kubeconfig/internal/git"
)

const (
	errNotRemoteCluster  = "managed resource is not a RemoteCluster custom resource"
	errTrackPCUsage      = "cannot track ProviderConfig usage"
	errGetPC             = "cannot get ProviderConfig"
	errGetCPC            = "cannot get ClusterProviderConfig"
	errGetGitSecret      = "cannot get Git auth secret"
	errGetDecryptSecret  = "cannot get decryption key secret"
	errCloneRepo         = "cannot clone/pull git repository"
	errReadFile          = "cannot read file from git repository"
	errDecryptFile       = "cannot decrypt file"
	errCreateSecret      = "cannot create kubeconfig Secret"
	errGetSecret         = "cannot get kubeconfig Secret"
	errUpdateSecret      = "cannot update kubeconfig Secret"
	errDeleteSecret      = "cannot delete kubeconfig Secret"
	errCreateProviderCfg = "cannot create downstream ProviderConfig"
	errDeleteProviderCfg = "cannot delete downstream ProviderConfig"
	errListProviderCfg   = "cannot list downstream ProviderConfigs"

	annotationContentHash  = "kubeconfig.stuttgart-things.com/content-hash"
	labelManagedBy         = "app.kubernetes.io/managed-by"
	labelRemoteCluster     = "remotecluster.kubeconfig.stuttgart-things.com/name"
	defaultSecretNamespace = "crossplane-system"
)

var providerConfigGVK = map[string]schema.GroupVersionKind{
	"provider-kubernetes": {Group: "kubernetes.crossplane.io", Version: "v1alpha1", Kind: "ProviderConfig"},
	"provider-helm":       {Group: "helm.crossplane.io", Version: "v1beta1", Kind: "ProviderConfig"},
}

// SetupGated adds a controller that reconciles RemoteCluster managed resources with safe-start support.
func SetupGated(mgr ctrl.Manager, o controller.Options) error {
	o.Gate.Register(func() {
		if err := Setup(mgr, o); err != nil {
			panic(errors.Wrap(err, "cannot setup RemoteCluster controller"))
		}
	}, v1alpha1.RemoteClusterGroupVersionKind)
	return nil
}

func Setup(mgr ctrl.Manager, o controller.Options) error {
	name := managed.ControllerName(v1alpha1.RemoteClusterGroupKind)

	opts := []managed.ReconcilerOption{
		managed.WithExternalConnector(&connector{
			kube:  mgr.GetClient(),
			usage: resource.NewProviderConfigUsageTracker(mgr.GetClient(), &apisv1alpha1.ClusterProviderConfigUsage{}),
		}),
		managed.WithLogger(o.Logger.WithValues("controller", name)),
		managed.WithPollInterval(o.PollInterval),
		managed.WithRecorder(event.NewAPIRecorder(mgr.GetEventRecorderFor(name))),
	}

	if o.Features.Enabled(feature.EnableBetaManagementPolicies) {
		opts = append(opts, managed.WithManagementPolicies())
	}

	if o.Features.Enabled(feature.EnableAlphaChangeLogs) {
		opts = append(opts, managed.WithChangeLogger(o.ChangeLogOptions.ChangeLogger))
	}

	if o.MetricOptions != nil {
		opts = append(opts, managed.WithMetricRecorder(o.MetricOptions.MRMetrics))
	}

	if o.MetricOptions != nil && o.MetricOptions.MRStateMetrics != nil {
		stateMetricsRecorder := statemetrics.NewMRStateRecorder(
			mgr.GetClient(), o.Logger, o.MetricOptions.MRStateMetrics, &v1alpha1.RemoteClusterList{}, o.MetricOptions.PollStateMetricInterval,
		)
		if err := mgr.Add(stateMetricsRecorder); err != nil {
			return errors.Wrap(err, "cannot register MR state metrics recorder for kind v1alpha1.RemoteClusterList")
		}
	}

	r := managed.NewReconciler(mgr, resource.ManagedKind(v1alpha1.RemoteClusterGroupVersionKind), opts...)

	return ctrl.NewControllerManagedBy(mgr).
		Named(name).
		WithOptions(o.ForControllerRuntime()).
		WithEventFilter(resource.DesiredStateChanged()).
		For(&v1alpha1.RemoteCluster{}).
		Complete(ratelimiter.NewReconciler(name, r, o.GlobalRateLimiter))
}

type connector struct {
	kube  client.Client
	usage *resource.ProviderConfigUsageTracker
}

func (c *connector) Connect(ctx context.Context, mg resource.Managed) (managed.ExternalClient, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return nil, errors.New(errNotRemoteCluster)
	}

	if err := c.usage.Track(ctx, cr); err != nil {
		return nil, errors.Wrap(err, errTrackPCUsage)
	}

	spec, err := c.resolveProviderConfigSpec(ctx, cr)
	if err != nil {
		return nil, err
	}

	gitToken, err := c.resolveGitToken(ctx, spec)
	if err != nil {
		return nil, err
	}

	ageKey, err := c.resolveAgeKey(ctx, spec)
	if err != nil {
		return nil, err
	}

	return &external{kube: c.kube, providerSpec: *spec, gitToken: gitToken, ageKey: ageKey}, nil
}

func (c *connector) resolveProviderConfigSpec(ctx context.Context, cr *v1alpha1.RemoteCluster) (*apisv1alpha1.ProviderConfigSpec, error) {
	ref := cr.GetProviderConfigReference()
	if ref == nil {
		return nil, errors.New("providerConfigRef is not set")
	}

	switch ref.Kind {
	case "ProviderConfig":
		pc := &apisv1alpha1.ProviderConfig{}
		if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name}, pc); err != nil {
			return nil, errors.Wrap(err, errGetPC)
		}
		return &pc.Spec, nil
	case "ClusterProviderConfig":
		cpc := &apisv1alpha1.ClusterProviderConfig{}
		if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name}, cpc); err != nil {
			return nil, errors.Wrap(err, errGetCPC)
		}
		return &cpc.Spec, nil
	default:
		return nil, errors.Errorf("unsupported provider config kind: %s", ref.Kind)
	}
}

func (c *connector) resolveGitToken(ctx context.Context, spec *apisv1alpha1.ProviderConfigSpec) (string, error) {
	if spec.Git.SecretRef == nil {
		return "", nil
	}
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{
		Name:      spec.Git.SecretRef.Name,
		Namespace: spec.Git.SecretRef.Namespace,
	}, secret); err != nil {
		return "", errors.Wrap(err, errGetGitSecret)
	}
	return string(secret.Data["token"]), nil
}

func (c *connector) resolveAgeKey(ctx context.Context, spec *apisv1alpha1.ProviderConfigSpec) (string, error) {
	decryptRef := spec.Decryption.SecretRef
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{
		Name:      decryptRef.Name,
		Namespace: decryptRef.Namespace,
	}, secret); err != nil {
		return "", errors.Wrap(err, errGetDecryptSecret)
	}
	return string(secret.Data["key"]), nil
}

type external struct {
	kube         client.Client
	providerSpec apisv1alpha1.ProviderConfigSpec
	gitToken     string
	ageKey       string
}

// secretName returns a deterministic Secret name for the RemoteCluster.
func secretName(crName string) string {
	return fmt.Sprintf("kubeconfig-%s", crName)
}

// contentHash returns the hex-encoded SHA-256 hash of data.
func contentHash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// cloneAndReadFile clones/pulls the Git repo, reads the file at source.path,
// and decrypts it if an age key is configured.
func (c *external) cloneAndReadFile(ctx context.Context, filePath string) ([]byte, error) {
	repo := gitpkg.NewRepo(c.providerSpec.Git.URL, c.providerSpec.Git.Branch, c.gitToken)

	if _, err := repo.EnsureCloned(ctx); err != nil {
		return nil, errors.Wrap(err, errCloneRepo)
	}

	content, err := repo.ReadFile(filePath)
	if err != nil {
		return nil, errors.Wrap(err, errReadFile)
	}

	// Decrypt if an age key is available
	if c.ageKey != "" {
		decrypted, err := decryptpkg.SOPSDecrypt(content, filePath, c.ageKey)
		if err != nil {
			return nil, errors.Wrap(err, errDecryptFile)
		}
		return decrypted, nil
	}

	return content, nil
}

// buildDownstreamProviderConfig builds an unstructured downstream ProviderConfig
// for provider-kubernetes or provider-helm.
func buildDownstreamProviderConfig(pcRef v1alpha1.ProviderConfigRef, secretName, secretNamespace, crName string) (*unstructured.Unstructured, error) {
	gvk, ok := providerConfigGVK[pcRef.Type]
	if !ok {
		return nil, errors.Errorf("unsupported downstream provider type: %s", pcRef.Type)
	}

	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	u.SetName(pcRef.Name)
	u.SetLabels(map[string]string{
		labelManagedBy:     "provider-kubeconfig",
		labelRemoteCluster: crName,
	})

	if err := unstructured.SetNestedMap(u.Object, map[string]interface{}{
		"source": "Secret",
		"secretRef": map[string]interface{}{
			"name":      secretName,
			"namespace": secretNamespace,
			"key":       "kubeconfig",
		},
	}, "spec", "credentials"); err != nil {
		return nil, errors.Wrap(err, "cannot set credentials in downstream ProviderConfig")
	}

	return u, nil
}

// ensureDownstreamProviderConfigs creates any missing downstream ProviderConfigs
// and deletes stale ones that are no longer in the spec.
func (c *external) ensureDownstreamProviderConfigs(ctx context.Context, cr *v1alpha1.RemoteCluster, sName, sNamespace string) error {
	desired := make(map[string]v1alpha1.ProviderConfigRef)
	for _, pc := range cr.Spec.ForProvider.ProviderConfigs {
		desired[pc.Name] = pc

		u, err := buildDownstreamProviderConfig(pc, sName, sNamespace, cr.GetName())
		if err != nil {
			return err
		}

		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(u.GroupVersionKind())
		err = c.kube.Get(ctx, types.NamespacedName{Name: pc.Name}, existing)
		if kerrors.IsNotFound(err) {
			if err := c.kube.Create(ctx, u); err != nil {
				return errors.Wrapf(err, "%s %q", errCreateProviderCfg, pc.Name)
			}
			continue
		}
		if err != nil {
			return errors.Wrapf(err, "cannot check downstream ProviderConfig %q", pc.Name)
		}
	}

	// Delete stale ProviderConfigs that have our label but are no longer desired
	for _, gvk := range providerConfigGVK {
		if err := c.deleteStaleProviderConfigs(ctx, gvk, cr.GetName(), desired); err != nil {
			return err
		}
	}

	return nil
}

// deleteStaleProviderConfigs deletes ProviderConfigs with the ownership label
// that are no longer in the desired set.
func (c *external) deleteStaleProviderConfigs(ctx context.Context, gvk schema.GroupVersionKind, crName string, desired map[string]v1alpha1.ProviderConfigRef) error {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(gvk)
	selector := labels.SelectorFromSet(labels.Set{
		labelRemoteCluster: crName,
	})
	if err := c.kube.List(ctx, list, &client.ListOptions{LabelSelector: selector}); err != nil {
		if !kerrors.IsNotFound(err) {
			return errors.Wrap(err, errListProviderCfg)
		}
		return nil
	}
	for i := range list.Items {
		item := &list.Items[i]
		if _, ok := desired[item.GetName()]; !ok {
			if err := c.kube.Delete(ctx, item); err != nil && !kerrors.IsNotFound(err) {
				return errors.Wrapf(err, "%s %q", errDeleteProviderCfg, item.GetName())
			}
		}
	}
	return nil
}

// deleteAllDownstreamProviderConfigs deletes all ProviderConfigs owned by this CR.
func (c *external) deleteAllDownstreamProviderConfigs(ctx context.Context, crName string) error {
	for _, gvk := range providerConfigGVK {
		if err := c.deleteStaleProviderConfigs(ctx, gvk, crName, nil); err != nil {
			return err
		}
	}
	return nil
}

// downstreamProviderConfigsUpToDate checks that all desired downstream ProviderConfigs exist.
func (c *external) downstreamProviderConfigsUpToDate(ctx context.Context, cr *v1alpha1.RemoteCluster) bool {
	for _, pc := range cr.Spec.ForProvider.ProviderConfigs {
		gvk, ok := providerConfigGVK[pc.Type]
		if !ok {
			return false
		}
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		if err := c.kube.Get(ctx, types.NamespacedName{Name: pc.Name}, u); err != nil {
			return false
		}
	}
	return true
}

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRemoteCluster)
	}

	ns := cr.Spec.ForProvider.SecretNamespace
	if ns == "" {
		ns = defaultSecretNamespace
	}
	name := secretName(cr.GetName())

	secret := &corev1.Secret{}
	err := c.kube.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, secret)
	if kerrors.IsNotFound(err) {
		return managed.ExternalObservation{ResourceExists: false}, nil
	}
	if err != nil {
		return managed.ExternalObservation{}, errors.Wrap(err, errGetSecret)
	}

	// Detect drift: pull latest from Git and compare content hash
	upToDate := true
	content, err := c.cloneAndReadFile(ctx, cr.Spec.ForProvider.Source.Path)
	if err != nil {
		return managed.ExternalObservation{}, err
	}
	currentHash := contentHash(content)
	storedHash := secret.GetAnnotations()[annotationContentHash]
	if currentHash != storedHash {
		upToDate = false
	}

	// Check that all downstream ProviderConfigs exist
	if upToDate && !c.downstreamProviderConfigsUpToDate(ctx, cr) {
		upToDate = false
	}

	cr.SetConditions(xpv2.Available())
	obs := v1alpha1.RemoteClusterObservation{
		ClusterName: cr.GetName(),
		SecretRef:   name,
	}

	// Best-effort: gather remote cluster info from the kubeconfig
	if info, err := clusterpkg.Gather(ctx, secret.Data["kubeconfig"]); err == nil {
		obs.ServerVersion = info.ServerVersion
		obs.APIEndpoint = info.APIEndpoint
		obs.PodCIDR = info.PodCIDR
		obs.ServiceCIDR = info.ServiceCIDR
		obs.NodeCIDRs = info.NodeCIDRs
		obs.NodeCount = info.NodeCount
	}

	cr.Status.AtProvider = obs

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: upToDate,
		ConnectionDetails: managed.ConnectionDetails{
			"kubeconfig": secret.Data["kubeconfig"],
		},
	}, nil
}

func (c *external) Create(ctx context.Context, mg resource.Managed) (managed.ExternalCreation, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return managed.ExternalCreation{}, errors.New(errNotRemoteCluster)
	}

	ns := cr.Spec.ForProvider.SecretNamespace
	if ns == "" {
		ns = defaultSecretNamespace
	}
	name := secretName(cr.GetName())

	// Clone/pull Git repo and read the kubeconfig file
	content, err := c.cloneAndReadFile(ctx, cr.Spec.ForProvider.Source.Path)
	if err != nil {
		return managed.ExternalCreation{}, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels: map[string]string{
				labelManagedBy:     "provider-kubeconfig",
				labelRemoteCluster: cr.GetName(),
			},
			Annotations: map[string]string{
				annotationContentHash: contentHash(content),
			},
		},
		Data: map[string][]byte{
			"kubeconfig": content,
		},
	}

	if err := c.kube.Create(ctx, secret); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateSecret)
	}

	// Create downstream ProviderConfigs
	if err := c.ensureDownstreamProviderConfigs(ctx, cr, name, ns); err != nil {
		return managed.ExternalCreation{}, err
	}

	cr.Status.AtProvider = v1alpha1.RemoteClusterObservation{
		ClusterName: cr.GetName(),
		SecretRef:   name,
	}

	return managed.ExternalCreation{
		ConnectionDetails: managed.ConnectionDetails{
			"kubeconfig": content,
		},
	}, nil
}

func (c *external) Update(ctx context.Context, mg resource.Managed) (managed.ExternalUpdate, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return managed.ExternalUpdate{}, errors.New(errNotRemoteCluster)
	}

	ns := cr.Spec.ForProvider.SecretNamespace
	if ns == "" {
		ns = defaultSecretNamespace
	}
	name := secretName(cr.GetName())

	// Clone/pull Git repo and read the kubeconfig file
	content, err := c.cloneAndReadFile(ctx, cr.Spec.ForProvider.Source.Path)
	if err != nil {
		return managed.ExternalUpdate{}, err
	}

	// Fetch and update the existing Secret
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, secret); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errGetSecret)
	}

	secret.Data["kubeconfig"] = content
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	secret.Annotations[annotationContentHash] = contentHash(content)

	if err := c.kube.Update(ctx, secret); err != nil {
		return managed.ExternalUpdate{}, errors.Wrap(err, errUpdateSecret)
	}

	// Reconcile downstream ProviderConfigs (create missing, delete stale)
	if err := c.ensureDownstreamProviderConfigs(ctx, cr, name, ns); err != nil {
		return managed.ExternalUpdate{}, err
	}

	return managed.ExternalUpdate{
		ConnectionDetails: managed.ConnectionDetails{
			"kubeconfig": content,
		},
	}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotRemoteCluster)
	}

	ns := cr.Spec.ForProvider.SecretNamespace
	if ns == "" {
		ns = defaultSecretNamespace
	}
	name := secretName(cr.GetName())

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
	}
	if err := c.kube.Delete(ctx, secret); err != nil && !kerrors.IsNotFound(err) {
		return managed.ExternalDelete{}, errors.Wrap(err, errDeleteSecret)
	}

	// Delete all downstream ProviderConfigs owned by this CR
	if err := c.deleteAllDownstreamProviderConfigs(ctx, cr.GetName()); err != nil {
		return managed.ExternalDelete{}, err
	}

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}
