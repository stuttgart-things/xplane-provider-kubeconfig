# provider-kubeconfig

`provider-kubeconfig` is a [Crossplane](https://crossplane.io/) Provider that
manages remote Kubernetes cluster kubeconfigs. It reads SOPS-encrypted
kubeconfig files from a Git repository, decrypts them, and bootstraps Secrets
and downstream ProviderConfigs for the remote clusters.

## Features

- **Git-based kubeconfig management** — clones/pulls a Git repo and reads encrypted kubeconfig files
- **SOPS/age decryption** — decrypts kubeconfigs encrypted with [SOPS](https://github.com/getsops/sops) using [age](https://age-encryption.org/) keys
- **Drift detection** — compares SHA-256 content hashes on every poll; automatically updates the Secret when the Git file changes
- **Downstream ProviderConfigs** — automatically creates `provider-kubernetes` and `provider-helm` ProviderConfig/ClusterProviderConfig resources referencing the kubeconfig Secret
- **ArgoCD cluster secrets** — optionally creates ArgoCD-compatible cluster secrets
- **Cluster type detection** — auto-detects Kubernetes distribution (`kind`, `k3s`, `rke2`, `k8s`) from server version and node metadata
- **Remote cluster status** — gathers metadata from the remote cluster (version, type, API endpoint, node count, CIDRs, internal network key) and exposes it in `status.atProvider`
- **Stale git cache recovery** — automatically re-clones when pull fails with stale objects
- **Structured logging** — logs key events (git clone, decryption, secret creation, downstream provisioning) for easier debugging
- **RBAC self-bootstrap** — automatically creates/updates the ClusterRole and ClusterRoleBinding for downstream ProviderConfig management on startup

## Custom Resource Types

| Kind | Scope | Description |
|------|-------|-------------|
| `ProviderConfig` | Namespaced | Git + SOPS/age decryption settings (namespaced) |
| `ClusterProviderConfig` | Cluster | Git + SOPS/age decryption settings (cluster-scoped) |
| `RemoteCluster` | Cluster | Managed resource — decrypts kubeconfig, creates Secret + downstream ProviderConfigs |

## Quick Start

### 1. Install the Provider

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-kubeconfig
spec:
  package: ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:v0.10.0
```

### 2. Create the Secrets

Create the age decryption key Secret (the key field must be named `key`):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: age-key
  namespace: crossplane-system
stringData:
  key: AGE-SECRET-KEY-1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

For private Git repos, create a token Secret (the field must be named `token`):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: git-credentials
  namespace: crossplane-system
stringData:
  token: ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

### 3. Create a ClusterProviderConfig

For a **public** repo (no git token needed):

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  git:
    url: https://github.com/my-org/my-public-repo.git
    branch: main
  decryption:
    provider: sops
    secretRef:
      name: age-key
      namespace: crossplane-system
```

For a **private** repo:

```yaml
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: my-private-repo
spec:
  git:
    url: https://github.com/my-org/my-private-repo.git
    branch: main
    secretRef:
      name: git-credentials
      namespace: crossplane-system
  decryption:
    provider: sops
    secretRef:
      name: age-key-private
      namespace: crossplane-system
```

> **Note:** Each repo/key combination needs its own ClusterProviderConfig. Multiple RemoteClusters can reference the same ClusterProviderConfig.

### 4. Create a RemoteCluster

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
        apiVersions:
          - v2-cluster
      - name: my-cluster-helm
        type: provider-helm
        apiVersions:
          - v2-cluster
  providerConfigRef:
    name: default
    kind: ClusterProviderConfig
```

#### API Version Labels

The `apiVersions` field controls which downstream ProviderConfig types are created:

| Label | API Group | Kind | Scope |
|-------|-----------|------|-------|
| `v1` | `*.crossplane.io` | `ProviderConfig` | Cluster |
| `v2` | `*.m.crossplane.io` | `ProviderConfig` | Namespaced |
| `v2-cluster` | `*.m.crossplane.io` | `ClusterProviderConfig` | Cluster |

Use `v2-cluster` for Crossplane v2+ setups.

### 5. Verify

```shell
$ kubectl get remotecluster
NAME                READY   SYNCED   CLUSTER              VERSION        TYPE   AGE
my-remote-cluster   True    True     my-remote-cluster    v1.35.1+k3s1   k3s    5m

# With wide output to see the NETWORK column
$ kubectl get remotecluster -o wide
NAME                READY   SYNCED   CLUSTER              VERSION        TYPE   NETWORK      AGE
my-remote-cluster   True    True     my-remote-cluster    v1.35.1+k3s1   k3s    10.31.102    5m
```

### 6. Use the Kubeconfig

Extract the decrypted kubeconfig to your local machine:

```shell
kubectl get secret kubeconfig-my-remote-cluster \
  -n crossplane-system -o jsonpath='{.data.kubeconfig}' | base64 -d > ~/.kube/my-remote-cluster

kubectl --kubeconfig ~/.kube/my-remote-cluster get nodes
```

## Cluster Type Detection

The provider auto-detects the Kubernetes distribution and writes it to `status.atProvider.clusterType`:

| Distribution | Detection Method |
|-------------|-----------------|
| `k3s` | Server version contains `+k3s` |
| `rke2` | Server version contains `+rke2` |
| `kind` | Node name ends with `-control-plane` or `-worker`, and providerID is empty or starts with `kind://` |
| `k8s` | Default fallback |

## Encrypting a Kubeconfig with SOPS/age

```shell
# Generate an age key pair
age-keygen -o age.key
# Public key: age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx

# Encrypt the kubeconfig
sops encrypt --age age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx \
  kubeconfig.yaml > kubeconfig.enc.yaml

# Store the secret key in Kubernetes (key field must be named "key")
kubectl create secret generic age-key \
  --namespace crossplane-system \
  --from-literal=key="$(cat age.key | grep AGE-SECRET-KEY)"
```

## RBAC

The provider bootstraps its own RBAC on startup to manage downstream ProviderConfigs. It creates:

- **ClusterRole** `provider-kubeconfig-downstream` — permissions for `providerconfigs` and `clusterproviderconfigs` in `kubernetes.m.crossplane.io`, `helm.m.crossplane.io`, and the legacy `*.crossplane.io` APIs
- **ClusterRoleBinding** `provider-kubeconfig-downstream` — binds to the provider's service account (auto-detected from the pod)

This is automatic — no manual RBAC setup required. On provider upgrades (new pod/SA name), the bootstrap appends the new SA to the binding.

## Building

### Prerequisites

- Go 1.23+
- Docker
- Make

### Build the Provider

```shell
# Initialize the build submodule (first time only)
make submodules

# Generate CRDs, deepcopy, and run linters
make reviewable

# Build the provider binary and Docker image
make build
```

### Local Development

```shell
# Create a kind cluster, install CRDs, and start the provider
make dev

# Clean up
make dev-clean
```

### Running Tests

```shell
go test ./internal/... -v -count=1
```

## Project Structure

```
apis/
  v1alpha1/                  # ProviderConfig, ClusterProviderConfig and usage types
  kubeconfig/
    v1alpha1/                # RemoteCluster managed resource type
internal/
  cluster/                   # Remote cluster info gathering (version, type, nodes, CIDRs)
  controller/
    config/                  # ProviderConfig controller
    remotecluster/           # RemoteCluster reconciler
    kubeconfig.go            # Controller registration (SetupGated)
  decrypt/                   # SOPS/age decryption
  git/                       # Git clone/pull with caching and stale recovery
  rbac/                      # RBAC self-bootstrap for downstream access
package/
  crds/                      # Generated CRDs
  crossplane.yaml            # Crossplane package metadata
```

## Provider Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--debug` / `-d` | `false` | Enable debug logging (shows V(1) verbose logs) |
| `--leader-election` / `-l` | `false` | Enable leader election for HA |
| `--poll` | `1m` | How often to check each resource for drift |
| `--sync` / `-s` | `1h` | Controller manager sync period |
| `--max-reconcile-rate` | `10` | Max reconciliations per second |

## Links

- [Crossplane Provider Development Guide](https://github.com/crossplane/crossplane/blob/master/contributing/guide-provider-development.md)
- [SOPS](https://github.com/getsops/sops)
- [age encryption](https://age-encryption.org/)
