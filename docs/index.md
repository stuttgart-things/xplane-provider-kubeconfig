# provider-kubeconfig

Crossplane Provider that manages remote Kubernetes cluster kubeconfigs. It reads SOPS-encrypted kubeconfig files from a Git repository, decrypts them, and bootstraps Secrets and downstream ProviderConfigs for the remote clusters.

## Features

- **Git-based kubeconfig management** -- clones/pulls a Git repo and reads kubeconfig files (encrypted or plain)
- **SOPS/age decryption** -- optionally decrypts kubeconfigs encrypted with SOPS using age keys; plain kubeconfigs are supported without decryption
- **Drift detection** -- compares SHA-256 content hashes on every poll; automatically updates the Secret when the Git file changes
- **Downstream ProviderConfigs** -- automatically creates `provider-kubernetes` and `provider-helm` ProviderConfig resources referencing the kubeconfig Secret
- **Remote cluster status** -- gathers metadata from the remote cluster (Kubernetes version, API endpoint, node count, CIDRs, internal network key)

## How It Works

When you create a `RemoteCluster` resource, the provider performs these 5 steps:

1. **Clone the Git repo** referenced in the `ClusterProviderConfig` and read the kubeconfig file at the specified path
2. **Decrypt** the kubeconfig using the SOPS/age key from the referenced Secret (skipped for unencrypted files)
3. **Create a Kubernetes Secret** named `kubeconfig-<remotecluster-name>` containing the decrypted kubeconfig
4. **Create downstream ProviderConfigs** (optional) for `provider-kubernetes` and/or `provider-helm` that reference the kubeconfig Secret
5. **Populate status** with remote cluster metadata (Kubernetes version, API endpoint, node count, CIDRs, internal network key)

On every poll interval (default 1m), the provider re-reads the Git file, compares the content hash, and updates the Secret if the kubeconfig has changed.

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────────┐
│  Git Repo    │────>│  Provider    │────>│  kubeconfig Secret   │
│  (encrypted) │     │  (decrypt)   │     └──────────────────────┘
└──────────────┘     │              │     ┌──────────────────────┐
                     │              │────>│  ProviderConfig      │
┌──────────────┐     │              │     │  (provider-k8s)      │
│  age-key     │────>│              │     └──────────────────────┘
│  Secret      │     │              │     ┌──────────────────────┐
└──────────────┘     │              │────>│  ProviderConfig      │
                     │              │     │  (provider-helm)     │
                     └──────────────┘     └──────────────────────┘
```

## Quick Start

### Step 1: Install the Provider

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-kubeconfig
spec:
  package: ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:v0.2.1  # or :latest
```

### Step 2: Create the age decryption key Secret

> **Note:** The decryption Secret must exist even when using unencrypted kubeconfigs. For plain kubeconfigs, create the Secret with an empty key -- the provider will skip decryption automatically.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: age-key
  namespace: crossplane-system
stringData:
  key: AGE-SECRET-KEY-1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

### Step 3: Create a ClusterProviderConfig

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  git:
    url: https://github.com/my-org/my-cluster-configs.git
    branch: main
    secretRef:                    # optional, for private repos
      name: git-credentials
      namespace: crossplane-system
  decryption:
    provider: sops
    secretRef:
      name: age-key
      namespace: crossplane-system
```

### Step 4: Create a RemoteCluster

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

### Step 5: Verify

```bash
$ kubectl get remotecluster
NAME                READY   SYNCED   CLUSTER              VERSION   AGE
my-remote-cluster   True    True     my-remote-cluster    v1.31.4   5m

# With wide output to see the NETWORK column (internalNetworkKey)
$ kubectl get remotecluster -o wide
NAME                READY   SYNCED   CLUSTER              VERSION   NETWORK      AGE
my-remote-cluster   True    True     my-remote-cluster    v1.31.4   10.31.102    5m

$ kubectl get secret kubeconfig-my-remote-cluster -n crossplane-system
NAME                             TYPE     DATA   AGE
kubeconfig-my-remote-cluster     Opaque   1      5m
```

## Custom Resource Types

| Kind | Scope | Description |
|------|-------|-------------|
| `ProviderConfig` | Namespaced | Git + SOPS/age decryption settings (namespaced) |
| `ClusterProviderConfig` | Cluster | Git + SOPS/age decryption settings (cluster-scoped) |
| `RemoteCluster` | Cluster | Managed resource -- decrypts kubeconfig, creates Secret + downstream ProviderConfigs |

## Encrypting a Kubeconfig

```bash
# Generate an age key pair
age-keygen -o age.key
# Public key: age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Encrypt the kubeconfig
sops encrypt --age age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  kubeconfig.yaml > kubeconfig.enc.yaml

# Store the secret key in Kubernetes
kubectl create secret generic age-key \
  --namespace crossplane-system \
  --from-file=key=age.key
```
