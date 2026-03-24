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
	"fmt"

	"github.com/crossplane/crossplane-runtime/v2/pkg/feature"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	decryptpkg "github.com/stuttgart-things/provider-kubeconfig/internal/decrypt"
	gitpkg "github.com/stuttgart-things/provider-kubeconfig/internal/git"
)

const (
	errNotRemoteCluster = "managed resource is not a RemoteCluster custom resource"
	errTrackPCUsage     = "cannot track ProviderConfig usage"
	errGetPC            = "cannot get ProviderConfig"
	errGetCPC           = "cannot get ClusterProviderConfig"
	errGetGitSecret     = "cannot get Git auth secret"
	errGetDecryptSecret = "cannot get decryption key secret"
	errCloneRepo        = "cannot clone/pull git repository"
	errReadFile         = "cannot read file from git repository"
	errDecryptFile      = "cannot decrypt file"
	errCreateSecret     = "cannot create kubeconfig Secret"
	errGetSecret        = "cannot get kubeconfig Secret"
	errDeleteSecret     = "cannot delete kubeconfig Secret"
)

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

	var spec apisv1alpha1.ProviderConfigSpec

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
		spec = pc.Spec
	case "ClusterProviderConfig":
		cpc := &apisv1alpha1.ClusterProviderConfig{}
		if err := c.kube.Get(ctx, types.NamespacedName{Name: ref.Name}, cpc); err != nil {
			return nil, errors.Wrap(err, errGetCPC)
		}
		spec = cpc.Spec
	default:
		return nil, errors.Errorf("unsupported provider config kind: %s", ref.Kind)
	}

	// Read Git auth token from the referenced Secret, if configured
	var gitToken string
	if spec.Git.SecretRef != nil {
		secret := &corev1.Secret{}
		if err := c.kube.Get(ctx, types.NamespacedName{
			Name:      spec.Git.SecretRef.Name,
			Namespace: spec.Git.SecretRef.Namespace,
		}, secret); err != nil {
			return nil, errors.Wrap(err, errGetGitSecret)
		}
		gitToken = string(secret.Data["token"])
	}

	// Read the decryption key from the referenced Secret
	var ageKey string
	decryptRef := spec.Decryption.SecretRef
	secret := &corev1.Secret{}
	if err := c.kube.Get(ctx, types.NamespacedName{
		Name:      decryptRef.Name,
		Namespace: decryptRef.Namespace,
	}, secret); err != nil {
		return nil, errors.Wrap(err, errGetDecryptSecret)
	}
	ageKey = string(secret.Data["key"])

	return &external{kube: c.kube, providerSpec: spec, gitToken: gitToken, ageKey: ageKey}, nil
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

func (c *external) Observe(ctx context.Context, mg resource.Managed) (managed.ExternalObservation, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return managed.ExternalObservation{}, errors.New(errNotRemoteCluster)
	}

	ns := cr.Spec.ForProvider.SecretNamespace
	if ns == "" {
		ns = "crossplane-system"
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

	cr.Status.AtProvider = v1alpha1.RemoteClusterObservation{
		ClusterName: cr.GetName(),
		SecretRef:   name,
	}

	return managed.ExternalObservation{
		ResourceExists:   true,
		ResourceUpToDate: true,
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
		ns = "crossplane-system"
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
				"app.kubernetes.io/managed-by":                       "provider-kubeconfig",
				"remotecluster.kubeconfig.stuttgart-things.com/name": cr.GetName(),
			},
		},
		Data: map[string][]byte{
			"kubeconfig": content,
		},
	}

	if err := c.kube.Create(ctx, secret); err != nil {
		return managed.ExternalCreation{}, errors.Wrap(err, errCreateSecret)
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
	return managed.ExternalUpdate{}, nil
}

func (c *external) Delete(ctx context.Context, mg resource.Managed) (managed.ExternalDelete, error) {
	cr, ok := mg.(*v1alpha1.RemoteCluster)
	if !ok {
		return managed.ExternalDelete{}, errors.New(errNotRemoteCluster)
	}

	ns := cr.Spec.ForProvider.SecretNamespace
	if ns == "" {
		ns = "crossplane-system"
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

	return managed.ExternalDelete{}, nil
}

func (c *external) Disconnect(ctx context.Context) error {
	return nil
}
