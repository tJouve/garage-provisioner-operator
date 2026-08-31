# Troubleshooting

This page lists common operator issues and how to diagnose them quickly.

## 1) Store not Ready

Symptoms:

- `GarageStore` or `GarageClusterStore` has `Ready=False`
- Buckets/keys stay in waiting state

Common causes:

- `spec.adminURL` invalid
- Admin token Secret missing
- Secret key `token` missing in referenced Secret
- Garage endpoint unreachable
- Invalid/expired admin token

Checks:

```bash
kubectl get garagestores.smokingcat.net -A
kubectl get garageclusterstores.smokingcat.net
kubectl describe garagestore <name> -n <ns>
kubectl describe garageclusterstore <name>
kubectl get secret <secret-name> -n <secret-namespace> -o yaml
```

Look for condition reasons:

- `SecretNotFound`
- `InvalidURL`
- `HealthCheckFailed`

## 2) Bucket creation fails

Symptoms:

- `GarageBucket` condition reason `CreateFailed`

Common causes:

- Store is not Ready
- Quota values invalid for Garage deployment policies
- Alias conflict (`globalAlias` already in use)

Checks:

```bash
kubectl describe garagebucket <name> -n <ns>
kubectl get garagestore <store> -n <ns> -o yaml
kubectl logs -n garage-provisioner-operator-system deploy/garage-provisioner-operator-controller-manager -c manager
```

## 3) Bucket deletion stuck

Symptoms:

- Bucket has deletion timestamp but remains present
- Condition reason `BucketNotEmpty`

Why:

- `deletionPolicy: Delete` requires empty bucket before remote delete

Fix:

1. Remove objects/uploads from the bucket in Garage
2. Wait for next reconcile (~30s)

Alternative:

- Use `deletionPolicy: Retain` if you want CR deletion without remote bucket deletion

## 4) Key permissions not syncing

Symptoms:

- `GarageKey` condition `UpdateFailed`
- Access does not match `spec.bucketPermissions`

Common causes:

- Referenced bucket missing/not ready
- Store not Ready
- Garage API permission call failed

Checks:

```bash
kubectl describe garagekey <name> -n <ns>
kubectl describe garagebucket <name> -n <ns>
kubectl logs -n garage-provisioner-operator-system deploy/garage-provisioner-operator-controller-manager -c manager
```

## 5) Secret not created

Symptoms:

- `GarageKey` condition `SecretCreationFailed`
- No credentials Secret in namespace

Common causes:

- Missing permission to create/update Secrets (RBAC)
- Invalid owner reference setup
- Missing secret access key from Garage response

Checks:

```bash
kubectl get secrets -n <ns>
kubectl auth can-i create secrets --as system:serviceaccount:garage-provisioner-operator-system:garage-provisioner-operator-controller-manager -n <ns>
kubectl describe garagekey <name> -n <ns>
```

## 6) Connection timeouts

Symptoms:

- health checks fail repeatedly
- errors like timeout/refused connection

Common causes:

- DNS or network policy blocks operator -> Garage
- Endpoint host/port wrong
- TLS/proxy mismatch

Checks:

```bash
kubectl logs -n garage-provisioner-operator-system deploy/garage-provisioner-operator-controller-manager -c manager
kubectl get networkpolicy -A
```

Also test Garage health endpoint outside operator path if possible.

## Debugging checklist

1. Inspect CR status conditions:

```bash
kubectl get garageclusterstores.smokingcat.net,garagestores.smokingcat.net,garagebuckets.smokingcat.net,garagekeys.smokingcat.net -A -o wide
```

2. Describe failing object:

```bash
kubectl describe <kind> <name> -n <namespace>
```

3. Read controller logs:

```bash
kubectl logs -n garage-provisioner-operator-system deploy/garage-provisioner-operator-controller-manager -c manager --tail=200
```

4. Validate referenced resources exist:

- Store -> Secret
- Bucket -> Store
- Key -> Bucket

5. Confirm Garage endpoint health and token validity.

---

Back to [README](../README.md) · See [Architecture](ARCHITECTURE.md)
