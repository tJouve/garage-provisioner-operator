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
GarageClusterStore (namespaced, globally referenceable) OR GarageStore (namespace scope)
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
  - Namespaced (globally referenceable)
  - Reusable from many namespaces
  - `spec.adminSecretRef` defaults Secret namespace to GarageClusterStore namespace
  - `spec.adminSecretRef.namespace` can override Secret namespace when needed

- **GarageStore**
  - Namespaced
  - Intended for tenant-local credentials and isolation
  - `spec.adminSecretRef` defaults Secret namespace to GarageStore namespace
  - `spec.adminSecretRef.namespace` can override Secret namespace when needed

Both are namespaced. `GarageClusterStore` is globally referenceable; `GarageStore` is same-namespace-only.

Both `GarageClusterStore` and `GarageStore` read admin token from `spec.adminSecretRef`.

### GarageBucket relationship

- `GarageBucket.spec.garageStoreRef` selects store resolution by kind:
  - `kind: GarageStore`:
    - defaults namespace to bucket namespace
    - uses explicit `garageStoreRef.namespace` when provided
  - `kind: GarageClusterStore`:
    - defaults namespace to `operators`
    - uses explicit `garageStoreRef.namespace` when provided

Bucket reconciliation waits until referenced store reports Ready.

### GarageKey relationship

- `GarageKey.spec.bucketRef.name` points to a `GarageBucket`
- `GarageKey.spec.bucketRef.namespace` is optional:
  - defaults to GarageKey namespace when omitted
  - uses explicit namespace when provided
- Key reconciliation waits until bucket has `status.bucketId`
- Key then resolves the bucket's store reference for Garage API access
- On successful create/update, key reconciliation keeps `status.accessKeyId` synchronized from Garage API responses

## Reconciliation patterns

## 1) Store health check flow

For both `GarageClusterStore` and `GarageStore`:

1. Read `spec.adminURL`
2. Read S3 credentials metadata from:
   - `spec.zone`
   - `spec.s3EndpointURL`
3. Resolve `spec.adminSecretRef`:
   - default Secret namespace = store resource namespace
   - optional override via `spec.adminSecretRef.namespace`
4. Build Garage admin client
5. Call Garage health endpoint
6. Update:
   - `status.ready`
   - `status.lastValidation`
   - Ready condition

Operator has read-only access to Secrets for this flow (`get`), aligned with least-privilege RBAC.

Requeue strategy:

- transient failures: ~30 seconds
- authentication failures: longer backoff (~5 minutes)
- success path uses `spec.refreshInterval` when set, defaulting to 24h

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

- `zone` (from resolved store `spec.zone`)
- `s3_endpoint_url` (from resolved store `spec.s3EndpointURL`)
- `access_key_id` (from Garage key response)
- `secret_access_key` (from Garage key response)

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
