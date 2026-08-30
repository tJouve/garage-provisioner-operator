# Local Development Guide

This guide covers local development workflows for `garage-provisioner-operator`.

Two runtime modes supported:

- **Mode 1 (direct):** run operator process on host with `make run`
- **Mode 2 (containerized):** run operator deployment inside Kind cluster via `config/overlays/dev`

## Prerequisites

- Go **1.22+**
- Kubebuilder
- Kind
- kubectl
- Docker

Optional but useful:

- `kustomize` (Makefile can install local copy in `./bin`)
- `docker-compose` (for local Garage option)

## Development files added

- `config/dev/kind-config.yaml`
- `config/dev/docker-compose-garage.yaml`
- `config/dev/samples/kustomization.yaml`
- `config/overlays/dev/kustomization.yaml`
- `config/overlays/dev/patches/deployment-patch.yaml`
- `config/overlays/dev/patches/image-patch.yaml`
- `config/overlays/dev/samples/*`

## Make targets for local dev

| Target | What it does |
|---|---|
| `make kind-create` | Create Kind cluster `garage-dev` from `config/dev/kind-config.yaml` |
| `make kind-delete` | Delete Kind cluster `garage-dev` |
| `make kind-load-image` | Load `DEV_IMG` (default `garage-operator:dev`) into Kind |
| `make dev-deploy` | Deploy operator using `config/overlays/dev` |
| `make dev-samples` | Apply sample resources from `config/dev/samples` |
| `make dev-logs` | Stream controller manager logs from cluster deployment |
| `make dev-clean` | Delete dev samples and dev deployment resources |

Configurable vars:

- `DEV_KIND_CLUSTER` (default: `garage-dev`)
- `DEV_IMG` (default: `garage-operator:dev`)
- `DEV_OVERLAY` (default: `config/overlays/dev`)
- `DEV_SAMPLES` (default: `config/dev/samples`)

Example override:

```bash
make kind-load-image DEV_IMG=localhost:5000/garage-operator:dev
```

---

## Mode 1: Run operator directly from host

Fastest feedback loop for controller code changes.

### 1) Create local Kind cluster

```bash
make kind-create
```

### 2) Point `KUBECONFIG` to Kind cluster

```bash
export KUBECONFIG="$(pwd)/.kubeconfig-garage-dev"
kind get kubeconfig --name garage-dev > "$KUBECONFIG"
kubectl get nodes
```

### 3) Install CRDs/RBAC/manager namespace resources

For direct mode, install manifests once before running:

```bash
make dev-deploy
```

### 4) Stop in-cluster manager deployment (avoid double-reconcile)

```bash
kubectl -n garage-provisioner-operator-system scale deployment garage-provisioner-operator-controller-manager --replicas=0
```

### 5) Run operator locally

```bash
make run
```

Logs print in terminal directly.

Verbose logging:

```bash
make run -- --zap-log-level debug
```

### 6) Apply sample CRs from another terminal

```bash
kubectl apply -k config/dev/samples
```

---

## Mode 2: Run operator in Kind (containerized)

Use this mode to validate full in-cluster behavior.

### 1) Create Kind cluster

```bash
kind create cluster --name garage-dev --config config/dev/kind-config.yaml
```

Or:

```bash
make kind-create
```

### 2) Build image

```bash
make docker-build IMG=localhost:5000/garage-operator:dev
```

If using overlay default image (`garage-operator:dev`), also tag alias:

```bash
docker tag localhost:5000/garage-operator:dev garage-operator:dev
```

### 3) Load image into Kind

```bash
kind load docker-image localhost:5000/garage-operator:dev --name garage-dev
kind load docker-image garage-operator:dev --name garage-dev
```

Or with Make target:

```bash
make kind-load-image DEV_IMG=garage-operator:dev
```

### 4) Deploy dev overlay

```bash
kubectl apply -k config/overlays/dev
```

Or:

```bash
make dev-deploy
```

### 5) Apply sample CRs

```bash
make dev-samples
```

---

## Full workflow example

```bash
# 1. Create Kind cluster
kind create cluster --name garage-dev --config config/dev/kind-config.yaml

# 2. Start operator (direct mode for faster iteration)
make run

# 3. In another terminal, apply test manifests
kubectl apply -k config/dev/samples

# 4. Watch reconciliation
kubectl get garageclusterstores,garagestores,garagebuckets,garagekeys -A -w
kubectl logs -n garage-provisioner-operator-system deployment/garage-provisioner-operator-controller-manager -f
```

Note: If you run direct mode (`make run`) and cluster deployment at same time, two controllers reconcile same resources. Prefer one mode at a time.

---

## Local Garage instance

### Option A: Use existing Garage instance

If Garage admin API already available, point store resource to existing endpoint and token Secret.

### Option B: Run single-node Garage via Docker Compose

Compose file:

- `config/dev/docker-compose-garage.yaml`

Start:

```bash
docker-compose -f config/dev/docker-compose-garage.yaml up -d
```

Stop:

```bash
docker-compose -f config/dev/docker-compose-garage.yaml down
```

Admin endpoint:

- `http://localhost:3903`

S3 endpoint:

- `http://localhost:3904`

---

## Dev sample manifests

Samples live under:

- `config/overlays/dev/samples/`

Applied via:

- `config/dev/samples/kustomization.yaml`

Included examples:

- `garage-admin-secret.yaml`
- `garagestore-local.yaml`
- `garagebucket-sample.yaml`
- `garagekey-sample.yaml`

---

## Debugging tips

Enable verbose logs in direct mode:

```bash
make run -- --zap-log-level debug
```

Inspect specific resource:

```bash
kubectl describe garagebucket app-data -n storage
```

Check status conditions:

```bash
kubectl get garagebucket app-data -n storage -o jsonpath='{.status.conditions}'
```

Stream controller logs (in-cluster mode):

```bash
make dev-logs
```

Filter reconcile events:

```bash
kubectl logs -n garage-provisioner-operator-system deployment/garage-provisioner-operator-controller-manager -c manager | grep -i reconcile
```

---

## Common issues and fixes

### 1) kubeconfig not set

Symptoms:

- `The connection to the server localhost:8080 was refused`

Fix:

```bash
export KUBECONFIG="$(pwd)/.kubeconfig-garage-dev"
kind get kubeconfig --name garage-dev > "$KUBECONFIG"
kubectl cluster-info
```

### 2) Garage endpoint unreachable

Symptoms:

- `connection refused`
- `context deadline exceeded`
- Store `Ready=False`

Fix checks:

```bash
kubectl get garagestore local-garage -n garage-provisioner-operator-system -o yaml
kubectl describe garagestore local-garage -n garage-provisioner-operator-system
curl -v http://localhost:3903
```

If running in Kind and using host endpoint, test both `host.docker.internal` and `127.0.0.1` patterns depending on networking setup.

### 3) Secret not found

Symptoms:

- status condition reason like `SecretNotFound`

Fix checks:

```bash
kubectl get secret garage-admin-secret -n garage-provisioner-operator-system
kubectl get secret garage-admin-secret -n garage-provisioner-operator-system -o jsonpath='{.data.token}'
```

Ensure `spec.adminToken.name`, `spec.adminToken.key`, and optional `spec.adminToken.namespace` match actual Secret.

---

## Cleanup

Delete resources only:

```bash
make dev-clean
```

Delete Kind cluster:

```bash
kind delete cluster --name garage-dev
```

Or:

```bash
make kind-delete
```
