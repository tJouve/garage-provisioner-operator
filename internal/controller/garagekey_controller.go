/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	storagev1alpha1 "github.com/deuxfleurs-org/garage-provisioner-operator/api/v1alpha1"
	"github.com/deuxfleurs-org/garage-provisioner-operator/internal/garage"
)

// GarageKeyReconciler reconciles a GarageKey object.
type GarageKeyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

const (
	garageKeyFinalizer = "garagekey.smokingcat.net/finalizer"
	keyRequeueDelay    = 30 * time.Second
)

// +kubebuilder:rbac:groups=smokingcat.net,resources=garagekeys,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=smokingcat.net,resources=garagekeys/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=smokingcat.net,resources=garagekeys/finalizers,verbs=update
// +kubebuilder:rbac:groups=smokingcat.net,resources=garagebuckets,verbs=get;list;watch
// +kubebuilder:rbac:groups=smokingcat.net,resources=garagestores,verbs=get;list;watch
// +kubebuilder:rbac:groups=smokingcat.net,resources=garageclusterstores,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;patch

// Reconcile is part of the main kubernetes reconciliation loop.
// nolint:gocyclo
func (r *GarageKeyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	key := &storagev1alpha1.GarageKey{}
	if err := r.Get(ctx, req.NamespacedName, key); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	bucket, bucketID, err := r.resolveBucket(ctx, key)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "BucketNotFound", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not resolve GarageBucket", "namespace", key.Namespace, "name", key.Name)
		r.Recorder.Eventf(key, nil, corev1.EventTypeWarning, "BucketNotFound", "ResolveBucket", "%s", err.Error())
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	storeReady, storeURL, err := r.resolveStoreReadinessAndURL(ctx, key.Namespace, &bucket.Spec.ClusterRef)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "StoreNotReady", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not resolve store for GarageKey", "namespace", key.Namespace, "name", key.Name)
		r.Recorder.Eventf(key, nil, corev1.EventTypeWarning, "StoreNotReady", "ResolveStore", "%s", err.Error())
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}
	if !storeReady {
		message := "Referenced store is not ready"
		if statusErr := r.updateKeyStatusCondition(ctx, key, "StoreNotReady", message); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Info("Referenced store is not ready", "namespace", key.Namespace, "name", key.Name, "storeRef", bucket.Spec.ClusterRef.Name)
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	garageClient, err := LoadGarageClientFromStore(ctx, r.Client, &bucket.Spec.ClusterRef, key.Namespace)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	if !key.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, key, garageClient)
	}

	if !controllerutil.ContainsFinalizer(key, garageKeyFinalizer) {
		before := key.DeepCopy()
		controllerutil.AddFinalizer(key, garageKeyFinalizer)
		if err := r.Patch(ctx, key, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}

	if strings.TrimSpace(key.Status.AccessKeyID) == "" {
		return r.reconcileCreate(ctx, key, bucketID, storeURL, garageClient)
	}

	return r.reconcileUpdate(ctx, key, bucket, bucketID, storeURL, garageClient)
}

func (r *GarageKeyReconciler) reconcileCreate(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	bucketID string,
	storeURL string,
	garageClient *garage.GarageAdminClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	createReq := buildCreateKeyReq(key, bucketID)
	if createReq == nil {
		err := fmt.Errorf("could not build key create request")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	keyInfo, err := garageClient.CreateKey(ctx, *createReq)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not create Garage key", "namespace", key.Namespace, "name", key.Name)
		r.Recorder.Eventf(key, nil, corev1.EventTypeWarning, "CreateFailed", "CreateKey", "%s", err.Error())
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	accessKeyID := strings.TrimSpace(keyInfo.AccessKeyID)
	if accessKeyID == "" {
		accessKeyID = strings.TrimSpace(keyInfo.ID)
	}
	if accessKeyID == "" {
		err := fmt.Errorf("garage create key response did not include access key identifier")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}
	keyInfo.AccessKeyID = accessKeyID
	if err := r.persistAccessKeyID(ctx, key, accessKeyID); err != nil {
		return ctrl.Result{}, err
	}

	if strings.TrimSpace(keyInfo.SecretAccessKey) == "" && strings.TrimSpace(keyInfo.SecretKey) == "" {
		err := fmt.Errorf("garage create key response did not include secret access key")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	if err := updateKeyPermissions(ctx, garageClient, bucketID, accessKeyID, key.Spec.BucketPermissions, storagev1alpha1.BucketPermissions{}); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	secret := buildSecretFromKey(key, keyInfo, storeURL)
	if secret == nil {
		err := fmt.Errorf("could not build credentials Secret")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "SecretCreationFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}
	if err := controllerutil.SetControllerReference(key, secret, r.Scheme); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "SecretCreationFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	if err := r.applyCredentialsSecret(ctx, secret); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "SecretCreationFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not create credentials Secret", "namespace", key.Namespace, "secretName", secret.Name)
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	before := key.DeepCopy()
	now := metav1.Now()
	key.Status.AccessKeyID = accessKeyID
	key.Status.LastSyncTime = &now
	meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "KeyCreated",
		Message:            "Garage key created",
		ObservedGeneration: key.GetGeneration(),
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, key, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(key, nil, corev1.EventTypeNormal, "KeyCreated", "CreateKey", "Garage key created with access key id %s", accessKeyID)
	log.Info("Created Garage key", "namespace", key.Namespace, "name", key.Name, "accessKeyID", accessKeyID)
	return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
}

func (r *GarageKeyReconciler) reconcileUpdate(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	bucket *storagev1alpha1.GarageBucket,
	bucketID string,
	storeURL string,
	garageClient *garage.GarageAdminClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	keyInfo, err := garageClient.GetKeyInfo(ctx, key.Status.AccessKeyID)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not get Garage key info", "namespace", key.Namespace, "name", key.Name, "accessKeyID", key.Status.AccessKeyID)
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	if strings.TrimSpace(keyInfo.AccessKeyID) == "" {
		keyInfo.AccessKeyID = strings.TrimSpace(key.Status.AccessKeyID)
	}

	if strings.TrimSpace(keyInfo.SecretAccessKey) == "" && strings.TrimSpace(keyInfo.SecretKey) == "" {
		existingSecretAccessKey := r.getExistingSecretAccessKey(ctx, key)
		keyInfo.SecretAccessKey = existingSecretAccessKey
	}
	if strings.TrimSpace(keyInfo.SecretAccessKey) == "" && strings.TrimSpace(keyInfo.SecretKey) == "" {
		err := fmt.Errorf("secret access key unavailable for credentials Secret sync")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	currentPerms := extractCurrentBucketPermissions(keyInfo, bucketID, bucket.Name)
	if err := updateKeyPermissions(ctx, garageClient, bucketID, key.Status.AccessKeyID, key.Spec.BucketPermissions, currentPerms); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	secret := buildSecretFromKey(key, keyInfo, storeURL)
	if secret == nil {
		err := fmt.Errorf("could not build credentials Secret")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}
	if err := controllerutil.SetControllerReference(key, secret, r.Scheme); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	if err := r.applyCredentialsSecret(ctx, secret); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	before := key.DeepCopy()
	now := metav1.Now()
	key.Status.LastSyncTime = &now
	meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "KeyReady",
		Message:            "Garage key is in desired state",
		ObservedGeneration: key.GetGeneration(),
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, key, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(key, nil, corev1.EventTypeNormal, "KeyReady", "SyncKey", "Garage key is in desired state")
	log.Info("Reconciled Garage key", "namespace", key.Namespace, "name", key.Name, "accessKeyID", key.Status.AccessKeyID)
	return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
}

func (r *GarageKeyReconciler) reconcileDelete(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	garageClient *garage.GarageAdminClient,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(key, garageKeyFinalizer) {
		return ctrl.Result{}, nil
	}

	if strings.TrimSpace(key.Status.AccessKeyID) != "" {
		if err := garageClient.DeleteKey(ctx, key.Status.AccessKeyID); err != nil {
			if !isGarageNotFound(err) {
				if statusErr := r.updateKeyStatusCondition(ctx, key, "DeleteFailed", err.Error()); statusErr != nil {
					return ctrl.Result{}, statusErr
				}
				return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
			}
		}
	}

	secretName := computeSecretName(key)
	if secretName != "" {
		secret := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: secretName}, secret)
		if err == nil {
			if err := r.Delete(ctx, secret); err != nil && !apierrors.IsNotFound(err) {
				if statusErr := r.updateKeyStatusCondition(ctx, key, "DeleteFailed", err.Error()); statusErr != nil {
					return ctrl.Result{}, statusErr
				}
				return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
			}
		}
	}

	before := key.DeepCopy()
	controllerutil.RemoveFinalizer(key, garageKeyFinalizer)
	if err := r.Patch(ctx, key, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *GarageKeyReconciler) resolveBucket(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
) (*storagev1alpha1.GarageBucket, string, error) {
	bucketName := strings.TrimSpace(key.Spec.BucketRef.Name)
	if bucketName == "" {
		return nil, "", fmt.Errorf("spec.bucketRef.name is required")
	}

	bucket := &storagev1alpha1.GarageBucket{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: bucketName}, bucket); err != nil {
		return nil, "", err
	}

	bucketID := strings.TrimSpace(bucket.Status.BucketID)
	if bucketID == "" {
		return nil, "", fmt.Errorf("referenced GarageBucket is not yet provisioned")
	}

	return bucket, bucketID, nil
}

func (r *GarageKeyReconciler) applyCredentialsSecret(
	ctx context.Context,
	desired *corev1.Secret,
) error {
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Namespace: desired.Namespace, Name: desired.Name}, existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return r.Create(ctx, desired)
		}
		return err
	}

	before := existing.DeepCopy()
	existing.Type = desired.Type
	existing.Data = desired.Data
	existing.OwnerReferences = desired.OwnerReferences

	return r.Patch(ctx, existing, client.MergeFrom(before))
}

func (r *GarageKeyReconciler) persistAccessKeyID(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	accessKeyID string,
) error {
	if strings.TrimSpace(accessKeyID) == "" || key.Status.AccessKeyID == accessKeyID {
		return nil
	}

	before := key.DeepCopy()
	key.Status.AccessKeyID = accessKeyID
	return r.Status().Patch(ctx, key, client.MergeFrom(before))
}

func (r *GarageKeyReconciler) getExistingSecretAccessKey(ctx context.Context, key *storagev1alpha1.GarageKey) string {
	secretName := computeSecretName(key)
	if strings.TrimSpace(secretName) == "" {
		return ""
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: secretName}, secret); err != nil {
		return ""
	}

	return strings.TrimSpace(string(secret.Data["AWS_SECRET_ACCESS_KEY"]))
}

func (r *GarageKeyReconciler) resolveStoreReadinessAndURL(
	ctx context.Context,
	namespace string,
	storeRef *storagev1alpha1.GarageStoreReference,
) (bool, string, error) {
	if storeRef == nil || strings.TrimSpace(storeRef.Name) == "" {
		return false, "", fmt.Errorf("store reference is required")
	}

	storeName := strings.TrimSpace(storeRef.Name)
	nsStore := &storagev1alpha1.GarageStore{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: storeName}, nsStore); err == nil {
		return nsStore.Status.Ready, nsStore.Spec.AdminURL, nil
	} else if !apierrors.IsNotFound(err) {
		return false, "", err
	}

	clusterStore := &storagev1alpha1.GarageClusterStore{}
	if err := r.Get(ctx, client.ObjectKey{Name: storeName}, clusterStore); err != nil {
		return false, "", err
	}

	return clusterStore.Status.Ready, clusterStore.Spec.AdminURL, nil
}

func (r *GarageKeyReconciler) updateKeyStatusCondition(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	reason string,
	message string,
) error {
	before := key.DeepCopy()
	now := metav1.Now()

	meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: key.GetGeneration(),
		LastTransitionTime: now,
	})

	return r.Status().Patch(ctx, key, client.MergeFrom(before))
}

func extractCurrentBucketPermissions(
	keyInfo *garage.KeyInfo,
	bucketID string,
	bucketName string,
) storagev1alpha1.BucketPermissions {
	if keyInfo == nil {
		return storagev1alpha1.BucketPermissions{}
	}

	for _, lookup := range []string{bucketID, bucketName} {
		if lookup == "" {
			continue
		}
		if keyInfo.BucketPerms != nil {
			if perms, ok := keyInfo.BucketPerms[lookup]; ok {
				return storagev1alpha1.BucketPermissions{Read: perms.Read, Write: perms.Write, Owner: perms.Owner}
			}
		}
		if keyInfo.Permissions != nil {
			if perms, ok := keyInfo.Permissions[lookup]; ok {
				return storagev1alpha1.BucketPermissions{Read: perms.Read, Write: perms.Write, Owner: perms.Owner}
			}
		}
		if keyInfo.Buckets != nil {
			if bucketPerms, ok := keyInfo.Buckets[lookup]; ok {
				perms := bucketPerms.Permissions
				read := perms.Read || bucketPerms.Read
				write := perms.Write || bucketPerms.Write
				owner := perms.Owner || bucketPerms.Owner
				return storagev1alpha1.BucketPermissions{Read: read, Write: write, Owner: owner}
			}
		}
	}

	return storagev1alpha1.BucketPermissions{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GarageKeyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("garagekey-controller")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.GarageKey{}).
		Watches(
			&storagev1alpha1.GarageBucket{},
			handler.EnqueueRequestsFromMapFunc(r.mapBucketToKeys),
		).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.mapSecretToKeys),
		).
		Named("garagekey").
		Complete(r)
}

func (r *GarageKeyReconciler) mapBucketToKeys(ctx context.Context, obj client.Object) []reconcile.Request {
	bucket, ok := obj.(*storagev1alpha1.GarageBucket)
	if !ok {
		return nil
	}

	keyList := &storagev1alpha1.GarageKeyList{}
	if err := r.List(ctx, keyList, client.InNamespace(bucket.Namespace)); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range keyList.Items {
		item := &keyList.Items[i]
		if item.Spec.BucketRef.Name != bucket.Name {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: item.Namespace, Name: item.Name},
		})
	}

	return requests
}

func (r *GarageKeyReconciler) mapSecretToKeys(ctx context.Context, obj client.Object) []reconcile.Request {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}

	keyList := &storagev1alpha1.GarageKeyList{}
	if err := r.List(ctx, keyList, client.InNamespace(secret.Namespace)); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range keyList.Items {
		item := &keyList.Items[i]
		if computeSecretName(item) != secret.Name {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: item.Namespace, Name: item.Name},
		})
	}

	return requests
}
