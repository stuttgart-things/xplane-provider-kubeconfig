package apis

import (
	"k8s.io/apimachinery/pkg/runtime"

	kubeconfigv1alpha1 "github.com/stuttgart-things/provider-kubeconfig/apis/kubeconfig/v1alpha1"
	providerv1alpha1 "github.com/stuttgart-things/provider-kubeconfig/apis/v1alpha1"
)

func init() {
	AddToSchemes = append(AddToSchemes,
		providerv1alpha1.SchemeBuilder.AddToScheme,
		kubeconfigv1alpha1.SchemeBuilder.AddToScheme,
	)
}

var AddToSchemes runtime.SchemeBuilder

func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}