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

## Unit Tests

```bash
go test ./internal/... -v -count=1
```

## Lint

```bash
golangci-lint run ./...
```
