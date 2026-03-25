# provider-kubeconfig

Crossplane Provider that manages remote Kubernetes cluster kubeconfigs. It reads SOPS-encrypted kubeconfig files from a Git repository, decrypts them, and bootstraps Secrets and downstream ProviderConfigs for the remote clusters.

## Features

- **Git-based kubeconfig management** -- clones/pulls a Git repo and reads encrypted kubeconfig files
- **SOPS/age decryption** -- decrypts kubeconfigs encrypted with SOPS using age keys
- **Drift detection** -- compares SHA-256 content hashes on every poll; automatically updates the Secret when the Git file changes
- **Downstream ProviderConfigs** -- automatically creates `provider-kubernetes` and `provider-helm` ProviderConfig resources referencing the kubeconfig Secret
- **Remote cluster status** -- gathers metadata from the remote cluster (Kubernetes version, API endpoint, node count, CIDRs)

## Quick Start

### Install via Crossplane xpkg

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-kubeconfig
spec:
  package: ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:latest
```

### Create Secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: age-key
  namespace: crossplane-system
stringData:
  key: AGE-SECRET-KEY-1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

### Create a ClusterProviderConfig

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  git:
    url: https://github.com/my-org/my-cluster-configs.git
    branch: main
  decryption:
    provider: sops
    secretRef:
      name: age-key
      namespace: crossplane-system
```

### Create a RemoteCluster

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: RemoteCluster
metadata:
  name: my-remote-cluster
spec:
  forProvider:
    source:
      path: clusters/my-cluster/kubeconfig.enc.yaml
    secretNamespace: crossplane-system
    providerConfigs:
      - name: my-cluster-kubernetes
        type: provider-kubernetes
      - name: my-cluster-helm
        type: provider-helm
  providerConfigRef:
    name: default
    kind: ClusterProviderConfig
```

## Custom Resource Types

| Kind | Scope | Description |
|------|-------|-------------|
| `ProviderConfig` | Namespaced | Git + SOPS/age decryption settings (namespaced) |
| `ClusterProviderConfig` | Cluster | Git + SOPS/age decryption settings (cluster-scoped) |
| `RemoteCluster` | Cluster | Managed resource -- decrypts kubeconfig, creates Secret + downstream ProviderConfigs |
