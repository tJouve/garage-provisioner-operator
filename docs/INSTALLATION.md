# Installation Guide

## Prerequisites

- Kubernetes 1.20+
- `kubectl` configured for target cluster
- Garage admin endpoint reachable from operator Pod network
- Garage admin token available in Kubernetes Secret

## Install with Kustomize

Primary command requested for this project:

```bash
kubectl apply -k config/
```

If your local tree does not include `config/kustomization.yaml`, install using Kubebuilder default overlay:

```bash
kubectl apply -k config/default
```

## Verify installation

### 1) Check operator Pod/Deployment

```bash
kubectl get deploy -n garage-provisioner-operator-system
kubectl get pods -n garage-provisioner-operator-system
```

### 2) Check CRD registration

```bash
kubectl get crd
```

Expected CRDs:

- `garageclusterstores.smokingcat.net`
- `garagestores.smokingcat.net`
- `garagebuckets.smokingcat.net`
- `garagekeys.smokingcat.net`

### 3) Apply sample resources

```bash
kubectl apply -k config/samples/
```

## Upgrade notes

- Keep API version compatibility in mind (`v1alpha1` currently)
- Regenerate manifests when upgrading controller code:

```bash
make manifests generate
```

- Apply updated manifests with:

```bash
kubectl apply -k config/default
```

For production upgrades, prefer staged rollout and verify status conditions after deployment.

## Uninstall

Delete sample CRs first:

```bash
kubectl delete -k config/samples/
```

Remove controller deployment:

```bash
make undeploy
```

Remove CRDs:

```bash
make uninstall
```

If buckets use `deletionPolicy: Retain`, remote Garage resources remain after CR deletion by design.

---

Back to [README](../README.md) · See [Troubleshooting](TROUBLESHOOTING.md)
