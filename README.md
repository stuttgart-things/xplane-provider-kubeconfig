# provider-kubeconfig

`provider-kubeconfig` is a [Crossplane](https://crossplane.io/) Provider that
manages remote Kubernetes cluster kubeconfigs. It reads SOPS-encrypted
kubeconfig files from a Git repository, decrypts them, and bootstraps Secrets
and downstream ProviderConfigs for the remote clusters.

## Features

- **Git-based kubeconfig management** — clones/pulls a Git repo and reads encrypted kubeconfig files
- **SOPS/age decryption** — decrypts kubeconfigs encrypted with [SOPS](https://github.com/getsops/sops) using [age](https://age-encryption.org/) keys
- **Drift detection** — compares SHA-256 content hashes on every poll; automatically updates the Secret when the Git file changes
- **Downstream ProviderConfigs** — automatically creates `provider-kubernetes` and `provider-helm` ProviderConfig resources referencing the kubeconfig Secret
- **Remote cluster status** — gathers metadata from the remote cluster (Kubernetes version, API endpoint, node count, CIDRs, internal network key) and exposes it in `status.atProvider`

## Custom Resource Types

| Kind | Scope | Description |
|------|-------|-------------|
| `ProviderConfig` | Namespaced | Git + SOPS/age decryption settings (namespaced) |
| `ClusterProviderConfig` | Cluster | Git + SOPS/age decryption settings (cluster-scoped) |
| `RemoteCluster` | Cluster | Managed resource — decrypts kubeconfig, creates Secret + downstream ProviderConfigs |

## Quick Start

### 1. Install the Provider

**Via Crossplane xpkg (recommended):**

```shell
cat <<EOF | kubectl apply -f -
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-kubeconfig
spec:
  package: ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:latest
EOF
```

**Running the released image (out-of-cluster):**

```shell
export KUBECONFIG=~/.kube/dev
export VERSION=v0.8.0

# Install CRDs from the release tag
for crd in clusterproviderconfigs clusterproviderconfigusages providerconfigs providerconfigusages remoteclusters; do
  kubectl apply -f \
    "https://raw.githubusercontent.com/stuttgart-things/xplane-provider-kubeconfig/${VERSION}/package/crds/kubeconfig.stuttgart-things.com_${crd}.yaml"
done

# Run the provider using the released image
docker run --rm --network host \
  -v "${KUBECONFIG}:/kubeconfig:ro" \
  -e KUBECONFIG=/kubeconfig \
  ghcr.io/stuttgart-things/provider-kubeconfig:${VERSION} \
  --debug
```

### 2. Create the Secrets

Create the age decryption key Secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: age-key
  namespace: crossplane-system
stringData:
  key: AGE-SECRET-KEY-1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX
```

Optionally, create a Git credentials Secret (for private repos):

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
    secretNamespace: crossplane-system      # default
    providerConfigs:                        # optional
      - name: my-cluster-kubernetes
        type: provider-kubernetes
      - name: my-cluster-helm
        type: provider-helm
  providerConfigRef:
    name: default
    kind: ClusterProviderConfig
```

This will:
1. Clone the Git repo and read `clusters/my-cluster/kubeconfig.enc.yaml`
2. Decrypt it using the age key
3. Create a Secret `kubeconfig-my-remote-cluster` in `crossplane-system`
4. Create downstream ProviderConfigs `my-cluster-kubernetes` and `my-cluster-helm`
5. Populate status with remote cluster metadata

```shell
$ kubectl get remotecluster
NAME                READY   SYNCED   CLUSTER              VERSION   AGE
my-remote-cluster   True    True     my-remote-cluster    v1.31.4   5m

# With wide output to see the NETWORK column
$ kubectl get remotecluster -o wide
NAME                READY   SYNCED   CLUSTER              VERSION   NETWORK      AGE
my-remote-cluster   True    True     my-remote-cluster    v1.31.4   10.31.102    5m
```

## Encrypting a Kubeconfig with SOPS/age

```shell
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

### Build and Push the Crossplane Package

```shell
# Build the xpkg (includes Docker image)
make build

# The image is tagged as provider-kubeconfig:<version>
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
  cluster/                   # Remote cluster info gathering (version, nodes, CIDRs)
  controller/
    config/                  # ProviderConfig controller
    remotecluster/           # RemoteCluster reconciler
    kubeconfig.go            # Controller registration (SetupGated)
  decrypt/                   # SOPS/age decryption
  git/                       # Git clone/pull with caching
package/
  crds/                      # Generated CRDs
  crossplane.yaml            # Crossplane package metadata
examples/
  provider/                  # ClusterProviderConfig example
  sample/                    # RemoteCluster example
  testdata/                  # Test kubeconfigs (plain + encrypted)
```

## Provider Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--debug` / `-d` | `false` | Enable debug logging |
| `--leader-election` / `-l` | `false` | Enable leader election for HA |
| `--poll` | `1m` | How often to check each resource for drift |
| `--sync` / `-s` | `1h` | Controller manager sync period |
| `--max-reconcile-rate` | `10` | Max reconciliations per second |

## Links

- [Crossplane Provider Development Guide](https://github.com/crossplane/crossplane/blob/master/contributing/guide-provider-development.md)
- [SOPS](https://github.com/getsops/sops)
- [age encryption](https://age-encryption.org/)
