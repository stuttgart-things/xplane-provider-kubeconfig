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
	"net"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Info holds observed cluster metadata.
type Info struct {
	ServerVersion      string
	APIEndpoint        string
	PodCIDR            string
	ServiceCIDR        string
	NodeCIDRs          []string
	NodeCount          int
	InternalNetworkKey string
	ClusterType        string
}

// Gather connects to the remote cluster using the provided kubeconfig bytes
// and returns observed cluster metadata.
func Gather(ctx context.Context, kubeconfig []byte) (*Info, error) {
	cfg, err := clientcmd.RESTConfigFromKubeConfig(kubeconfig)
	if err != nil {
		return nil, errors.Wrap(err, "cannot build REST config from kubeconfig")
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "cannot create kubernetes clientset")
	}

	info := &Info{APIEndpoint: cfg.Host}

	if err := gatherVersion(cs, info); err != nil {
		return nil, err
	}

	if err := gatherNodeInfo(ctx, cs, info); err != nil {
		return nil, err
	}

	gatherServiceCIDR(ctx, cs, info)

	info.ClusterType = detectClusterType(info.ServerVersion, ctx, cs)

	return info, nil
}

func gatherVersion(cs kubernetes.Interface, info *Info) error {
	sv, err := cs.Discovery().ServerVersion()
	if err != nil {
		return errors.Wrap(err, "cannot get server version")
	}
	info.ServerVersion = sv.GitVersion
	return nil
}

func gatherNodeInfo(ctx context.Context, cs kubernetes.Interface, info *Info) error {
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return errors.Wrap(err, "cannot list nodes")
	}
	info.NodeCount = len(nodes.Items)
	for _, node := range nodes.Items {
		if node.Spec.PodCIDR != "" {
			info.NodeCIDRs = append(info.NodeCIDRs, node.Spec.PodCIDR)
		}
	}
	if len(nodes.Items) > 0 && nodes.Items[0].Spec.PodCIDR != "" {
		info.PodCIDR = nodes.Items[0].Spec.PodCIDR
	}

	if key := internalNetworkKey(nodes.Items); key != "" {
		info.InternalNetworkKey = key
	}

	return nil
}

// internalNetworkKey returns the first 3 octets of the first node's InternalIP
// (e.g. "10.31.102" or "172.18.0"), which serves as a network key for
// clusterbook IP reservations.
func internalNetworkKey(nodes []corev1.Node) string {
	for _, node := range nodes {
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				ip := net.ParseIP(addr.Address)
				if ip == nil {
					continue
				}
				ip = ip.To4()
				if ip == nil {
					continue
				}
				parts := strings.SplitN(addr.Address, ".", 4)
				if len(parts) >= 3 {
					return strings.Join(parts[:3], ".")
				}
			}
		}
	}
	return ""
}

// detectClusterType determines the Kubernetes distribution from the server
// version string and node metadata. Returns one of: kind, k3s, rke2, k8s.
func detectClusterType(serverVersion string, ctx context.Context, cs kubernetes.Interface) string {
	// Check server version for distribution markers
	if strings.Contains(serverVersion, "+k3s") {
		return "k3s"
	}
	if strings.Contains(serverVersion, "+rke2") {
		return "rke2"
	}

	// Check node names/labels for kind clusters
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, node := range nodes.Items {
			// kind nodes have names like "{cluster}-control-plane" or "{cluster}-worker"
			if strings.HasSuffix(node.Name, "-control-plane") || strings.HasSuffix(node.Name, "-worker") {
				// Verify by checking the container runtime — kind uses containerd
				// and the providerID is empty (no cloud provider)
				if node.Spec.ProviderID == "" {
					return "kind"
				}
			}
		}
	}

	return "k8s"
}

func gatherServiceCIDR(ctx context.Context, cs kubernetes.Interface, info *Info) {
	svc, err := cs.CoreV1().Services("default").Get(ctx, "kubernetes", metav1.GetOptions{})
	if err == nil && svc.Spec.ClusterIP != "" {
		info.ServiceCIDR = svc.Spec.ClusterIP + "/16"
	}
}
