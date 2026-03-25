# CI/CD

## GitHub Actions Workflows

| Workflow | Trigger | Description |
|----------|---------|-------------|
| `build-test` | Push/PR to main | Go build, unit tests, golangci-lint |
| `build-scan-image` | Push/PR to main | Build container image via Dagger, push to ttl.sh, scan with Trivy |
| `release` | After image scan on main | Semantic-release, push image + xpkg to ghcr.io |

## Release Process

Releases are automated via [semantic-release](https://semantic-release.gitbook.io/) using the `call-go-release.yaml` reusable workflow:

- `fix:` commits trigger a **patch** bump
- `feat:` commits trigger a **minor** bump
- `feat!:` or `BREAKING CHANGE:` commits trigger a **major** bump

Each release:

1. Creates a GitHub release with changelog
2. Tags the container image at `ghcr.io/stuttgart-things/provider-kubeconfig:<version>`
3. Builds and pushes the Crossplane xpkg to `ghcr.io/stuttgart-things/provider-kubeconfig-xpkg:<version>`

## Running Tests Locally

```bash
# Unit tests
go test ./internal/... -v -count=1

# Build
go build ./...

# Lint (requires golangci-lint)
golangci-lint run ./...

# Docker build
docker build -f cluster/images/provider-kubeconfig/Dockerfile -t provider-kubeconfig:test .
```
