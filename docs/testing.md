# Testing

## Kind Cluster Setup

### 1. Create a Kind cluster

```bash
kind create cluster --name dev
```

### 2. Install Cilium

```bash
kubectl apply -k https://github.com/stuttgart-things/helm/infra/crds/cilium

dagger call -m github.com/stuttgart-things/dagger/helm@v0.57.0 \
  helmfile-operation \
  --helmfile-ref "git::https://github.com/stuttgart-things/helm.git@infra/cilium.yaml.gotmpl" \
  --operation apply \
  --state-values "config=kind,clusterName=dev,configureLB=false" \
  --kube-config file:///home/sthings/.kube/dev \
  --progress plain -vv
```

### 3. Install Crossplane

```bash
dagger call -m github.com/stuttgart-things/dagger/helm@v0.57.0 \
  helmfile-operation \
  --helmfile-ref "git::https://github.com/stuttgart-things/helm.git@cicd/crossplane.yaml.gotmpl" \
  --operation apply \
  --state-values "version=2.2.0" \
  --kube-config file:///home/sthings/.kube/dev \
  --progress plain -vv
```

### 4. Install CRDs and run the provider

```bash
kubectl apply -R -f package/crds
go run cmd/provider/main.go --debug
```

## End-to-End Test with Released Provider Package

After the cluster and Crossplane are ready, install the released provider xpkg and test with the included testdata.

### 1. Install the provider

```yaml
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-kubeconfig
spec:
  package: ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:v0.2.1
```

```bash
kubectl get providers provider-kubeconfig
# Wait until INSTALLED=True and HEALTHY=True
```

### 2. Create the ClusterProviderConfig

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: age-key
  namespace: crossplane-system
stringData:
  key: ""
---
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: ClusterProviderConfig
metadata:
  name: default
spec:
  git:
    url: https://github.com/stuttgart-things/xplane-provider-kubeconfig.git
    branch: main
  decryption:
    provider: sops
    secretRef:
      name: age-key
      namespace: crossplane-system
EOF
```

### 3. Create a RemoteCluster using testdata

The repo includes test kubeconfigs in `examples/testdata/`:

| File | Description |
|------|-------------|
| `kubeconfig.yaml` | Unencrypted kubeconfig (works with empty age key) |
| `kubeconfig.enc.yaml` | SOPS-encrypted kubeconfig (requires matching age secret key) |

```bash
kubectl apply -f - <<'EOF'
apiVersion: kubeconfig.stuttgart-things.com/v1alpha1
kind: RemoteCluster
metadata:
  name: test-cluster
spec:
  forProvider:
    source:
      path: examples/testdata/kubeconfig.yaml
    secretNamespace: crossplane-system
  providerConfigRef:
    name: default
    kind: ClusterProviderConfig
EOF
```

### 4. Verify

```bash
# RemoteCluster should be Ready + Synced
kubectl get remotecluster test-cluster

# Kubeconfig Secret should exist
kubectl get secret kubeconfig-test-cluster -n crossplane-system

# Inspect the decrypted kubeconfig
kubectl get secret kubeconfig-test-cluster -n crossplane-system \
  -o jsonpath='{.data.kubeconfig}' | base64 -d
```

## Unit Tests

```bash
go test ./internal/... -v -count=1
```

## Lint

```bash
golangci-lint run ./...
```
