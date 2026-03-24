# provider-kubeconfig

`provider-kubeconfig` is a [Crossplane](https://crossplane.io/) Provider that
manages remote Kubernetes cluster kubeconfigs. It reads SOPS-encrypted
kubeconfig files from a Git repository, decrypts them, and bootstraps Secrets
and downstream ProviderConfigs for the remote clusters.

## Overview

### Custom Resource Types

| Kind | Scope | Description |
|------|-------|-------------|
| `ProviderConfig` | Namespaced | Git + SOPS/age decryption settings (namespaced) |
| `ClusterProviderConfig` | Cluster | Git + SOPS/age decryption settings (cluster-scoped) |
| `ProviderConfigUsage` | Namespaced | Tracks which managed resources use a `ProviderConfig` |
| `ClusterProviderConfigUsage` | Cluster | Tracks which managed resources use a `ClusterProviderConfig` |
| `RemoteCluster` | Cluster | Managed resource representing a remote cluster kubeconfig |

### Provider Config

The `ProviderConfig` / `ClusterProviderConfig` resources define how to connect
to the Git repository and how to decrypt secrets:

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  git:
    url: https://github.com/my-org/my-cluster-configs.git
    branch: main
    secretRef:
      name: git-credentials
      namespace: crossplane-system
  decryption:
    provider: sops
    secretRef:
      name: age-key
      namespace: crossplane-system
```

### RemoteCluster

The `RemoteCluster` managed resource points to an encrypted kubeconfig file
inside the Git repository:

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

## Project Structure

```
apis/
  v1alpha1/                  # ProviderConfig, ClusterProviderConfig and usage types
  kubeconfig/
    v1alpha1/                # RemoteCluster managed resource type
internal/
  controller/
    config/                  # ProviderConfig controller
    remotecluster/           # RemoteCluster reconciler (scaffold, no logic yet)
    kubeconfig.go            # Controller registration (SetupGated)
package/
  crds/                      # Generated CRDs
  crossplane.yaml            # Crossplane package metadata
```

## Development Status

The provider scaffold is complete and builds successfully. The `RemoteCluster`
controller currently contains **placeholder logic only** (no-op observe, create,
update, delete). The next steps are to implement the actual reconciliation:

1. Clone/pull the Git repository referenced in the `ProviderConfig`
2. Read the SOPS-encrypted kubeconfig file at the specified path
3. Decrypt the kubeconfig using the configured age/SOPS key
4. Create a Kubernetes Secret with the decrypted kubeconfig
5. Optionally create downstream `ProviderConfig` resources (e.g. for `provider-kubernetes`, `provider-helm`)
6. Populate `status.atProvider` with cluster metadata (server version, endpoint, CIDRs, node count)

## Developing

1. Initialize the build submodule:
   ```shell
   make submodules
   ```

2. Generate CRDs, deepcopy, and run linters:
   ```shell
   make reviewable
   ```

3. Build the provider binary and Docker image:
   ```shell
   make build
   ```

## Links

- [Crossplane Provider Development Guide](https://github.com/crossplane/crossplane/blob/master/contributing/guide-provider-development.md)
- [Crossplane Contributing Guide](https://github.com/crossplane/crossplane/blob/master/CONTRIBUTING.md)
