# Architecture

## System overview

Garage Provisioner Operator manages Garage S3 resources from Kubernetes Custom Resources.

At runtime, the operator runs several reconciliation loops:

- **GarageClusterStore reconciler**: validates shared Garage admin connectivity
- **GarageStore reconciler**: validates namespace-scoped Garage admin connectivity
- **GarageBucket reconciler**: provisions and synchronizes buckets
- **GarageKey reconciler**: provisions access keys, synchronizes permissions, and writes Secrets

Each loop is **idempotent**, meaning it can run multiple times safely and converge to desired state.

## CRD hierarchy and relationships

Resource graph:

```text
GarageClusterStore (cluster scope) OR GarageStore (namespace scope)
                             |
                             v
                    GarageBucket (namespace)
                             |
                             v
                     GarageKey (namespace)
                             |
                             v
             Secret (namespace, app credentials)
```

### GarageClusterStore vs GarageStore

- **GarageClusterStore**
  - Cluster-scoped
  - Reusable from many namespaces
  - Admin token Secret must be created in the `operators` namespace (where operator deploys)
  - GarageClusterStore references it by name only, no namespace needed

- **GarageStore**
  - Namespaced
  - Intended for tenant-local credentials and isolation

Both `GarageClusterStore` and `GarageStore` read admin token via Secret reference from the `operators` namespace only.

### GarageBucket relationship

- `GarageBucket.spec.clusterRef.name` points to:
  - a `GarageStore` in the same namespace, or
  - a `GarageClusterStore` with same name at cluster scope

Bucket reconciliation waits until referenced store reports Ready.

### GarageKey relationship

- `GarageKey.spec.bucketRef.name` points to a `GarageBucket` in same namespace
- Key reconciliation waits until bucket has `status.bucketId`
- Key then resolves the bucket's store reference for Garage API access

## Reconciliation patterns

## 1) Store health check flow

For both `GarageClusterStore` and `GarageStore`:

1. Read `spec.adminURL`
2. Resolve `spec.adminToken` from Secret in `operators` namespace
3. Build Garage admin client
4. Call Garage health endpoint
5. Update:
   - `status.ready`
   - `status.lastValidation`
   - Ready condition

Operator has read-only access to Secrets for this flow (`get`), aligned with least-privilege RBAC.

Requeue strategy:

- transient failures: ~30 seconds
- authentication failures: longer backoff (~5 minutes)

## 2) Bucket CRUD lifecycle

### Create

- Add finalizer `garagebucket.smokingcat.net/finalizer`
- Build create request from:
  - optional `globalAlias`
  - optional quotas (`maxSize`, `maxObjects`)
- Call Garage create API
- Store returned bucket identifier in `status.bucketId`
- Set Ready condition `BucketCreated`

### Update

- Read current remote bucket info
- Compare desired quotas vs current
- If drift detected, call update API
- Update `status.lastSyncTime`
- Set Ready condition `BucketReady`

### Delete

If finalizer exists:

- `deletionPolicy: Delete`:
  - verify bucket emptiness (objects/uploads count)
  - if non-empty: set condition `BucketNotEmpty`, requeue
  - if empty: delete remote bucket, remove finalizer
- `deletionPolicy: Retain`:
  - skip remote delete
  - remove finalizer

No force-deletion of non-empty buckets.

## 3) Key lifecycle and permission management

### Create

- Add finalizer `garagekey.smokingcat.net/finalizer`
- Create key in Garage
- Write `status.accessKeyId`
- Sync bucket permissions
- Create credentials Secret if it does not already exist
- Set Ready condition `KeyCreated`

### Update

- Verify key exists
- Compute permission delta (allow/deny)
- Apply permission changes
- Keep previously created credentials Secret unchanged
- Update `status.lastSyncTime`
- Set Ready condition `KeyReady`

### Delete

- Delete key in Garage (ignore not-found)
- Keep credentials Secret untouched
- Remove finalizer

## 4) Secret auto-sync flow

Generated Secret is namespaced and typed `Opaque`.

Fields:

- `AWS_ACCESS_KEY_ID`
- `AWS_SECRET_ACCESS_KEY`
- `AWS_S3_ENDPOINT`
- `AWS_REGION` (set to `garage`)
- `bucket_name`

Secret is created once by the operator and left unchanged if it already exists.

## Finalizers and garbage collection

Finalizers prevent premature CR deletion before external cleanup:

- `garagebucket.smokingcat.net/finalizer`
- `garagekey.smokingcat.net/finalizer`

Owner references are used where possible for in-cluster dependent resources (Secrets).

## Error handling and requeue strategy

Controller behavior:

- Records concise condition reason/message on failures
- Emits Kubernetes events for visibility
- Requeues to retry transient errors
- Distinguishes auth failures for slower retry in store controllers

This keeps reconciliation robust without tight error loops.

## Namespace isolation

Isolation rules:

- `GarageBucket` and `GarageKey` are namespaced
- `GarageKey` can only reference bucket in same namespace
- `GarageBucket` prefers namespaced `GarageStore`; cluster store is explicit fallback
- Generated Secret is always in same namespace as `GarageKey`

This supports multi-tenant deployment with clear boundaries.

## RBAC and least privilege

The operator uses split RBAC:

- **Namespace-scoped Role** (`operators` namespace):
  - Manages CRDs (GarageClusterStore, GarageStore, GarageBucket, GarageKey)
  - Reads admin token Secrets only in `operators` namespace (`get` permission)
  - Cannot read Secrets in other namespaces

- **Cluster-scoped ClusterRole**:
  - Creates credentials Secrets for GarageKey in any namespace (`create` permission)
  - Can read Secrets by name to verify creation (`get` permission)
  - No update/patch/watch/delete/list permissions (minimal attack surface)

Result: Each GarageKey can provision credentials in its own namespace with minimal permissions.
---

Back to [README](../README.md) · See [Examples](EXAMPLES.md)
