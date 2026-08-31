# Garage Provisioner Operator

Kubernetes operator for provisioning and managing [Garage](https://garagehq.deuxfleurs.fr/) S3 resources.

API group: `smokingcat.net/v1alpha1`

## What this operator manages

This project provides 4 Custom Resources (CRDs):

1. **GarageClusterStore** (namespaced, globally referenceable) – shared Garage admin endpoint and credentials
2. **GarageStore** (namespaced) – namespace-local Garage admin endpoint and credentials
3. **GarageBucket** (namespaced) – Garage bucket lifecycle and quotas
4. **GarageKey** (namespaced) – access key lifecycle, bucket permissions, and Kubernetes Secret sync

## Features

- Store health validation (`/health`) with Ready conditions
- Bucket lifecycle management: create, update, delete
- Bucket deletion policies:
  - `Delete` (delete remote bucket when empty)
  - `Retain` (keep remote bucket)
- Key lifecycle management and permission sync (`read`, `write`, `owner`)
- Automatic credential Secret creation/update with:
  - `zone`
  - `s3_endpoint_url`
  - `access_key_id`
  - `secret_access_key`
- Controller status conditions and event emission for debugging

## Quick start

### Prerequisites

- Kubernetes 1.20+
- `kubectl`
- `kustomize` support in `kubectl` (`kubectl apply -k ...`)
- A reachable Garage admin endpoint

### Install operator

```bash
kubectl apply -k config/
```

If your environment uses the default Kubebuilder layout without a root `config/kustomization.yaml`, use:

```bash
kubectl apply -k config/default
```

### Apply example resources

```bash
kubectl apply -k config/samples/
```

### Check status

```bash
kubectl get garageclusterstores.smokingcat.net,garagestores.smokingcat.net,garagebuckets.smokingcat.net,garagekeys.smokingcat.net -A
```

## Architecture (conceptual)

```text
           +-------------------------------+
           | Garage Admin API              |
           | (health, buckets, keys, ACL) |
           +---------------+---------------+
                           ^
                           | HTTP + Bearer token
        +------------------+------------------+
        | Kubernetes Operator (controller)    |
        | - GarageClusterStore reconciler     |
        | - GarageStore reconciler            |
        | - GarageBucket reconciler           |
        | - GarageKey reconciler              |
        +------------------+------------------+
                           |
      +--------------------+---------------------+
      |                                          |
+-----v----------------+          +--------------v---------------+
| Store CRDs           |          | Workload CRDs                |
| - GarageClusterStore |          | - GarageBucket               |
| - GarageStore        |          | - GarageKey                  |
+----------------------+          +--------------+---------------+
                                                  |
                                                  v
                                    +-----------------------------+
                                    | Kubernetes Secret           |
                                    | S3 credentials for apps     |
                                    +-----------------------------+
```

## Documentation

- [Installation Guide](docs/INSTALLATION.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Examples](docs/EXAMPLES.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Development Guide](docs/DEVELOPMENT.md)

## License

Apache-2.0
