# provider-kubeconfig

Crossplane Provider that manages remote Kubernetes cluster kubeconfigs. It reads kubeconfig files from **Git repositories** (SOPS-encrypted) or **HashiCorp Vault** (KVv2), and bootstraps Secrets and downstream ProviderConfigs for the remote clusters.

## Features

- **Dual source support** -- read kubeconfigs from Git+SOPS or Vault KVv2
- **Git-based kubeconfig management** -- clones/pulls a Git repo and reads kubeconfig files (encrypted or plain)
- **SOPS/age decryption** -- optionally decrypts kubeconfigs encrypted with SOPS using age keys
- **Vault KVv2 integration** -- reads kubeconfigs from Vault with Kubernetes auth or AppRole auth
- **Drift detection** -- content hash comparison for Git, KVv2 version tracking for Vault
- **Downstream ProviderConfigs** -- automatically creates `provider-kubernetes` and `provider-helm` ProviderConfig/ClusterProviderConfig resources
- **ArgoCD cluster secrets** -- optionally creates ArgoCD-compatible cluster secrets
- **Cluster type detection** -- auto-detects Kubernetes distribution (`kind`, `k3s`, `rke2`, `k8s`)
- **Remote cluster status** -- gathers metadata (version, type, API endpoint, node count, CIDRs, internal network key)
- **RBAC self-bootstrap** -- automatically manages its own ClusterRole/ClusterRoleBinding on startup

## How It Works

When you create a `RemoteCluster` resource, the provider performs these steps:

1. **Read the kubeconfig** from the configured source (Git repo or Vault KVv2)
2. **Decrypt** (Git+SOPS only) the kubeconfig using the age key from the referenced Secret
3. **Create a Kubernetes Secret** named `kubeconfig-<remotecluster-name>` containing the kubeconfig
4. **Create downstream ProviderConfigs** (optional) for `provider-kubernetes` and/or `provider-helm`
5. **Populate status** with remote cluster metadata (version, type, API endpoint, CIDRs, etc.)

On every poll interval (default 1m), the provider checks for changes and updates the Secret automatically.

### Git Source Flow

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────────┐
│  Git Repo    │────>│  Provider    │────>│  kubeconfig Secret   │
│  (encrypted) │     │  (decrypt)   │     └──────────────────────┘
└──────────────┘     │              │     ┌──────────────────────┐
                     │              │────>│  ClusterProviderCfg  │
┌──────────────┐     │              │     │  (provider-k8s)      │
│  age-key     │────>│              │     └──────────────────────┘
│  Secret      │     │              │     ┌──────────────────────┐
└──────────────┘     │              │────>│  ClusterProviderCfg  │
                     │              │     │  (provider-helm)     │
                     └──────────────┘     └──────────────────────┘
```

### Vault Source Flow

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────────┐
│  Vault KVv2  │────>│  Provider    │────>│  kubeconfig Secret   │
│  (plaintext) │     │  (k8s auth)  │     └──────────────────────┘
└──────────────┘     │              │     ┌──────────────────────┐
                     │              │────>│  ClusterProviderCfg  │
                     │              │     │  (provider-k8s)      │
                     │              │     └──────────────────────┘
                     │              │     ┌──────────────────────┐
                     │              │────>│  ClusterProviderCfg  │
                     │              │     │  (provider-helm)     │
                     └──────────────┘     └──────────────────────┘
```

## Quick Start: Git Source

### Step 1: Install the Provider

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-kubeconfig
spec:
  package: ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:v0.11.0
```

### Step 2: Create the age decryption key Secret

The key field must be named `key`:

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

For private repos, create a token Secret (field must be named `token`):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: git-credentials
  namespace: crossplane-system
stringData:
  token: ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

### Step 4: Create a RemoteCluster

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: RemoteCluster
metadata:
  name: my-cluster
spec:
  forProvider:
    source:
      path: clusters/my-cluster/kubeconfig.enc.yaml
    secretNamespace: crossplane-system
    providerConfigs:
      - name: my-cluster-kubernetes
        type: provider-kubernetes
        apiVersions: [v2-cluster]
      - name: my-cluster-helm
        type: provider-helm
        apiVersions: [v2-cluster]
  providerConfigRef:
    name: default
    kind: ClusterProviderConfig
```

## Quick Start: Vault Source

### Step 1: Store kubeconfig in Vault

```shell
vault kv put secret/clusters/my-cluster kubeconfig=@kubeconfig.yaml
```

### Step 2: Create a ClusterProviderConfig

**Kubernetes auth** (recommended -- zero credentials to manage):

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: vault-k8s
spec:
  vault:
    address: https://vault.example.com
    auth:
      method: kubernetes
      kubernetes:
        role: provider-kubeconfig
```

**AppRole auth** (for non-Kubernetes environments):

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: vault-approle
spec:
  vault:
    address: https://vault.example.com
    auth:
      method: approle
      appRole:
        roleId: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
        secretRef:
          name: vault-approle-secret
          namespace: crossplane-system
---
apiVersion: v1
kind: Secret
metadata:
  name: vault-approle-secret
  namespace: crossplane-system
stringData:
  secret-id: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
```

### Step 3: Create a RemoteCluster

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: RemoteCluster
metadata:
  name: my-cluster
spec:
  forProvider:
    source:
      type: vault
      path: clusters/my-cluster
      key: kubeconfig               # key within the KVv2 secret (default: kubeconfig)
    secretNamespace: crossplane-system
    providerConfigs:
      - name: my-cluster-kubernetes
        type: provider-kubernetes
        apiVersions: [v2-cluster]
      - name: my-cluster-helm
        type: provider-helm
        apiVersions: [v2-cluster]
  providerConfigRef:
    name: vault-k8s
    kind: ClusterProviderConfig
```

### Vault Configuration Options

| Field | Default | Description |
|-------|---------|-------------|
| `vault.address` | | Vault server URL (required) |
| `vault.namespace` | | Vault enterprise namespace (optional) |
| `vault.mountPath` | `secret` | KVv2 secrets engine mount path |
| `vault.auth.method` | | `kubernetes` or `approle` (required) |
| `vault.auth.kubernetes.role` | | Vault role for Kubernetes auth |
| `vault.auth.kubernetes.mountPath` | `kubernetes` | Vault auth mount path |
| `vault.auth.appRole.roleId` | | AppRole role ID |
| `vault.auth.appRole.mountPath` | `approle` | Vault auth mount path |
| `vault.auth.appRole.secretRef` | | Secret containing `secret-id` key |

### Vault Drift Detection

For Vault sources, the provider tracks the KVv2 metadata version number. When you update the secret in Vault (creating a new version), the provider detects the version change and updates the Kubernetes Secret automatically -- no content hashing needed.

## Verify

```bash
$ kubectl get remotecluster
NAME         READY   SYNCED   CLUSTER      VERSION        TYPE   AGE
my-cluster   True    True     my-cluster   v1.35.1+k3s1   k3s    5m

# Wide output shows NETWORK column
$ kubectl get remotecluster -o wide
NAME         READY   SYNCED   CLUSTER      VERSION        TYPE   NETWORK      AGE
my-cluster   True    True     my-cluster   v1.35.1+k3s1   k3s    10.31.102    5m

# Extract kubeconfig to local machine
kubectl get secret kubeconfig-my-cluster \
  -n crossplane-system -o jsonpath='{.data.kubeconfig}' | base64 -d > ~/.kube/my-cluster
```

## Custom Resource Types

| Kind | Scope | Description |
|------|-------|-------------|
| `ProviderConfig` | Namespaced | Git/Vault + decryption settings (namespaced) |
| `ClusterProviderConfig` | Cluster | Git/Vault + decryption settings (cluster-scoped) |
| `RemoteCluster` | Cluster | Managed resource -- reads kubeconfig, creates Secret + downstream ProviderConfigs |

## API Version Labels

The `apiVersions` field on `providerConfigs` controls which downstream ProviderConfig types are created:

| Label | API Group | Kind | Scope |
|-------|-----------|------|-------|
| `v1` | `*.crossplane.io` | `ProviderConfig` | Cluster |
| `v2` | `*.m.crossplane.io` | `ProviderConfig` | Namespaced |
| `v2-cluster` | `*.m.crossplane.io` | `ClusterProviderConfig` | Cluster |

Use `v2-cluster` for Crossplane v2+ setups.

## Cluster Type Detection

| Distribution | Detection Method |
|-------------|-----------------|
| `k3s` | Server version contains `+k3s` |
| `rke2` | Server version contains `+rke2` |
| `kind` | Node name suffix + empty or `kind://` providerID |
| `k8s` | Default fallback |

## Encrypting a Kubeconfig

```bash
# Generate an age key pair
age-keygen -o age.key

# Encrypt the kubeconfig
sops encrypt --age age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  kubeconfig.yaml > kubeconfig.enc.yaml

# Store the secret key in Kubernetes (key field must be named "key")
kubectl create secret generic age-key \
  --namespace crossplane-system \
  --from-literal=key="$(cat age.key | grep AGE-SECRET-KEY)"
```
