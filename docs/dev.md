# Provider Development

Scaffolding a new Crossplane provider from the template. Follow the steps exactly in order.

## Prerequisites

### angryjet

```bash
go install github.com/crossplane/crossplane-tools/cmd/angryjet@latest
```

## Init

### 1. Create the repo from the template

Go to [crossplane/provider-template](https://github.com/crossplane/provider-template) → click **Use this template** → create your repo.

```bash
git clone https://github.com/YOUR_ORG/provider-YOUR_SYSTEM
cd provider-YOUR_SYSTEM
```

### 2. Init the build submodule

```bash
make submodules
```

### 3. Rename the provider

```bash
export provider_name=Kubeconfig
make provider.prepare provider=${provider_name}
```

### 4. Add your first type

```bash
export group=kubeconfig
export type=RemoteCluster
make provider.addtype provider=Kubeconfig group=${group} kind=${type}
```

### 5. Fix API group domain

The template defaults to `kubeconfig.crossplane.io` and the `addtype` target introduces a double-prefix (`kubeconfig.kubeconfig.crossplane.io`). Replace both with your org domain:

```bash
# Replace base domain
find . -type f \( -name "*.go" -o -name "*.yaml" \) \
  | xargs grep -l "kubeconfig.crossplane.io" \
  | xargs sed -i 's|kubeconfig.crossplane.io|kubeconfig.stuttgart-things.com|g'

# Fix double-prefix introduced by addtype
find . -type f \( -name "*.go" -o -name "*.yaml" \) \
  | xargs grep -l "kubeconfig.kubeconfig.stuttgart-things.com" \
  | xargs sed -i 's|kubeconfig.kubeconfig.stuttgart-things.com|kubeconfig.stuttgart-things.com|g'
```

Verify no stale references remain:

```bash
grep -r "kubeconfig.kubeconfig\|crossplane.io" apis/ --include="*.go" | grep -v zz_generated
# expected: no output
```
