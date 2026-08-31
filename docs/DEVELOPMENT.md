# Development Guide

## Local setup

### Prerequisites

- Go (version from `go.mod`)
- Docker (for image builds)
- `kubectl`
- `kind` (for e2e)
- Kubebuilder v4

Clone and enter project:

```bash
git clone https://github.com/tJouve/garage-provisioner-operator.git
cd garage-provisioner-operator
```

Install tool dependencies on demand via Make targets.

## Build

Build manager binary:

```bash
make build
```

Build container image:

```bash
make docker-build IMG=<registry>/garage-provisioner-operator:<tag>
```

## Test

Unit/integration tests (envtest):

```bash
make test
```

Lint and auto-fix:

```bash
make lint-fix
```

E2E tests (requires isolated Kind cluster):

```bash
make test-e2e
```

## Development workflow

When changing API types (`api/v1alpha1/*_types.go`):

```bash
make manifests
make generate
```

When changing controllers (`internal/controller/*.go`):

```bash
make lint-fix
make test
```

## Code style and conventions

- Follow controller-runtime reconciliation patterns:
  - fetch current object
  - compute desired state
  - apply minimal changes
  - update status conditions
- Keep reconciliation idempotent
- Use structured logs with context keys
- Use Kubernetes `metav1.Condition` in statuses
- Keep error messages concise and actionable

## Adding new features

### 1) CRD changes

- Update type definitions in `api/v1alpha1/`
- Add kubebuilder validation/default markers
- Regenerate:

```bash
make manifests generate
```

### 2) Controller behavior

- Add reconciliation logic in `internal/controller/`
- Add watches and RBAC markers
- Add or update tests

## Commit message style

Use [Conventional Commits](https://www.conventionalcommits.org/):

- `feat: ...` new behavior
- `fix: ...` bug fix
- `docs: ...` documentation only
- `chore: ...` maintenance

Examples:

- `feat: add bucket retention policy handling`
- `fix: handle missing token Secret key`
- `docs: update API reference for GarageKey`

## Useful commands

```bash
make help
make run
make build-installer IMG=<registry>/garage-provisioner-operator:<tag>
```

---

Back to [README](../README.md) · See [Architecture](ARCHITECTURE.md)
