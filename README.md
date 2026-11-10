# Garage Provisioner Operator

Kubernetes operator for provisioning and managing [Garage](https://garagehq.deuxfleurs.fr/) S3 resources.

API group: `smokingcat.net/v1alpha1`

## Custom Resources

All four kinds are **namespaced**.

| Kind | Purpose |
| --- | --- |
| `GarageClusterStore` | Shared Garage admin endpoint + credentials. Resolved from the operator namespace (`operators`) unless `namespace` is set on the reference. |
| `GarageStore` | Namespace-local Garage admin endpoint + credentials. |
| `GarageBucket` | Bucket lifecycle, global alias and quotas. |
| `GarageKey` | Access key lifecycle, bucket permissions, and credentials Secret sync. |

### Store spec (`GarageStore` / `GarageClusterStore`)

```yaml
apiVersion: smokingcat.net/v1alpha1
kind: GarageStore
metadata:
  name: garagestore-sample
  namespace: garage
spec:
  adminURL: http://admin.garage.example.net:3903
  zone: garage
  s3EndpointURL: http://garage.example.net:3900
  adminSecretRef:
    name: garage-admin-secret
    namespace: garage      # optional, defaults to the store namespace
  refreshInterval: "24h"   # optional, default 24h
```

The referenced admin Secret must expose the Garage admin token under the key `token`.

Status reports `ready`, `lastValidation`, `observedGeneration` and a `Ready` condition based on a
call to the Garage admin health endpoint.

### `GarageBucket`

```yaml
apiVersion: smokingcat.net/v1alpha1
kind: GarageBucket
metadata:
  name: bucket-sample
  namespace: garage
spec:
  garageStoreRef:
    name: garagestore-sample
    kind: GarageStore            # GarageStore | GarageClusterStore
    namespace: garage            # optional
  globalAlias: bucket-sample     # optional
  quotas:                        # optional
    maxSize: 536870912000
    maxObjects: 10000000
  deletionPolicy: Delete         # Delete (default) | Retain
  refreshInterval: "24h"
```

Status reports `bucketId`, `lastSyncTime`, `observedGeneration` and a `Ready` condition.

With `deletionPolicy: Delete`, the finalizer keeps the resource around until the remote bucket is
empty; the reconciler emits a `BucketNotEmpty` event and retries. With `Retain`, the remote bucket
is left untouched.

### `GarageKey`

```yaml
apiVersion: smokingcat.net/v1alpha1
kind: GarageKey
metadata:
  name: bucket-sample-key
  namespace: garage
spec:
  bucketRef:
    name: bucket-sample
    namespace: garage            # optional, defaults to the GarageKey namespace
  bucketPermissions:
    read: true
    write: true
    owner: false
  secretRef:                     # optional, defaults to "<name>-credentials"
    name: bucket-sample-s3-credentials
  refreshInterval: "24h"
```

The store is resolved indirectly through the referenced `GarageBucket`. Status reports
`accessKeyId`, `lastSyncTime`, `observedGeneration` and a `Ready` condition.

#### Generated credentials Secret

The Secret is owned by the `GarageKey` (garbage-collected with it) and contains:

| Key | Value |
| --- | --- |
| `zone` | store `spec.zone` |
| `s3_endpoint_url` | store `spec.s3EndpointURL` |
| `access_key_id` | Garage access key id |
| `secret_access_key` | Garage secret access key |
| `bucket_name` | name of the referenced `GarageBucket` |

Missing or empty fields are repaired on the next reconciliation.

## Behaviour

- Store health validation against the Garage admin API with `Ready` conditions.
- Idempotent create/update/delete for buckets and keys, guarded by finalizers
  (`garagebucket.smokingcat.net/finalizer`, `garagekey.smokingcat.net/finalizer`).
- Permission sync (`read`, `write`, `owner`) between key and bucket.
- Periodic re-reconciliation driven by `spec.refreshInterval` (default `24h`); transient failures
  are retried after 30s and reported through conditions and Kubernetes events.
- Secrets are read directly from the API server (client cache disabled for `Secret`).

## Quick start

### Prerequisites

- A Kubernetes cluster and `kubectl`
- A reachable Garage admin endpoint and admin token
- Go and `make` to build from source

### Install

```bash
make install                                   # CRDs only
make deploy IMG=<registry>/<image>:<tag>       # CRDs + RBAC + manager
```

`make deploy` renders `config/default`, which deploys into the `operators` namespace with the
`garage-provisioner-operator-` name prefix. A standalone manifest can be produced with:

```bash
make build-installer IMG=<registry>/<image>:<tag>   # writes dist/install.yaml
```

### Apply example resources

```bash
kubectl apply -k config/samples/
```

The samples target the `garage` namespace and expect a `garage-admin-secret` Secret with a `token`
key. `sample_client.yaml` is an example Pod consuming the generated credentials Secret.

### Check status

```bash
kubectl get garageclusterstores,garagestores,garagebuckets,garagekeys -A
```

## Local development

```bash
make kind-create        # Kind cluster from config/dev/kind-config.yaml
make docker-build IMG=garage-operator:dev
make kind-load-image
make dev-deploy         # config/overlays/dev
make dev-samples        # config/dev/samples
make dev-logs
make dev-clean
```

Run the manager against the current kubeconfig context without deploying:

```bash
make run
```

## Testing

```bash
make test        # unit + envtest suites
make test-e2e    # e2e suite on a dedicated Kind cluster
make lint        # golangci-lint
```

## Architecture

```text
                    +--------------------------------+
                    | Garage Admin API               |
                    | health / buckets / keys / ACL  |
                    +---------------+----------------+
                                    ^
                                    | HTTP + Bearer token
                    +---------------+----------------+
                    | Controller manager             |
                    | - GarageClusterStore reconciler|
                    | - GarageStore reconciler       |
                    | - GarageBucket reconciler      |
                    | - GarageKey reconciler         |
                    +---------------+----------------+
                                    |
        +---------------------------+---------------------------+
        |                                                       |
+-------v--------------+                          +-------------v----------------+
| Store CRs            |  <---- referenced by ----| Workload CRs                 |
| - GarageClusterStore |                          | - GarageBucket               |
| - GarageStore        |                          | - GarageKey                  |
+----------------------+                          +-------------+----------------+
                                                                |
                                                                v
                                                  +-----------------------------+
                                                  | Secret (owned by GarageKey) |
                                                  | S3 credentials for apps     |
                                                  +-----------------------------+
```

## Documentation

- [Installation Guide](docs/INSTALLATION.md)
- [Architecture](docs/ARCHITECTURE.md)
- [Examples](docs/EXAMPLES.md)
- [Local Development](docs/LOCAL_DEVELOPMENT.md)
- [Development Guide](docs/DEVELOPMENT.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)

## License

Apache-2.0
