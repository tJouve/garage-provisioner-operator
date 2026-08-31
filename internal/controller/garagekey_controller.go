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

	storagev1alpha1 "github.com/tJouve/garage-provisioner-operator/api/v1alpha1"
	"github.com/tJouve/garage-provisioner-operator/internal/garage"
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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;create;update;patch
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
	specChanged := key.GetGeneration() != key.Status.ObservedGeneration
	refreshInterval := getRefreshInterval(key.Spec.RefreshInterval)

	bucket, bucketID, err := r.resolveBucket(ctx, key)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "BucketNotFound", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not resolve GarageBucket", "namespace", key.Namespace, "name", key.Name)
		r.Recorder.Eventf(key, nil, corev1.EventTypeWarning, "BucketNotFound", "ResolveBucket", "%s", err.Error())
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	storeReady, storeZone, storeS3EndpointURL, err := r.resolveStoreReadinessAndS3Config(ctx, key.Namespace, &bucket.Spec.GarageStoreRef)
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
		log.Info("Referenced store is not ready", "namespace", key.Namespace, "name", key.Name, "storeRef", bucket.Spec.GarageStoreRef.Name)
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	garageClient, err := LoadGarageClientFromStore(ctx, r.Client, &bucket.Spec.GarageStoreRef, key.Namespace)
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
		return r.reconcileCreate(ctx, key, bucketID, storeZone, storeS3EndpointURL, garageClient, refreshInterval, specChanged)
	}

	return r.reconcileUpdate(ctx, key, storeZone, storeS3EndpointURL, refreshInterval, specChanged)
}

func (r *GarageKeyReconciler) reconcileCreate(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	bucketID string,
	zone string,
	s3EndpointURL string,
	garageClient *garage.GarageAdminClient,
	refreshInterval time.Duration,
	specChanged bool,
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
	log.V(1).Info("Received Garage create key response", "keyInfo", keyInfoForLog(keyInfo))

	accessKeyID := strings.TrimSpace(keyInfo.AccessKeyID)
	if accessKeyID == "" {
		accessKeyID = strings.TrimSpace(keyInfo.ID)
	}
	secretAccessKey := keyInfo.GetSecretAccessKey()
	secretAccessKeyField := keyInfo.SecretAccessKeyField()
	if accessKeyID == "" || secretAccessKey == "" {
		err := fmt.Errorf("garage create key response did not include access key identifier and secret access key")
		if accessKeyID != "" {
			err = fmt.Errorf("garage create key response did not include secret access key")
		}
		if secretAccessKey != "" {
			err = fmt.Errorf("garage create key response did not include access key identifier")
		}
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(
			err,
			"Garage create key response missing required credentials fields",
			"namespace", key.Namespace,
			"name", key.Name,
			"secretField", secretAccessKeyField,
			"keyInfo", keyInfoForLog(keyInfo),
		)
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}
	keyInfo.AccessKeyID = accessKeyID
	log.V(1).Info("Resolved Garage secret field from create response", "secretField", secretAccessKeyField)
	if err := r.persistKeyCredentials(ctx, key, accessKeyID, secretAccessKey); err != nil {
		return ctrl.Result{}, err
	}

	if err := updateKeyPermissions(ctx, garageClient, bucketID, accessKeyID, key.Spec.BucketPermissions, storagev1alpha1.BucketPermissions{}); err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(
			err,
			"Could not apply desired bucket permissions to Garage key",
			"namespace", key.Namespace,
			"name", key.Name,
			"bucketID", bucketID,
			"accessKeyID", accessKeyID,
		)
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}
	log.V(1).Info(
		"Applied desired bucket permissions to Garage key",
		"namespace", key.Namespace,
		"name", key.Name,
		"bucketID", bucketID,
		"accessKeyID", accessKeyID,
	)

	secret, err := buildSecretFromKey(key, keyInfo, zone, s3EndpointURL, key.Spec.BucketRef.Name)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "SecretCreationFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(
			err,
			"Could not build credentials Secret from Garage create key response",
			"namespace", key.Namespace,
			"name", key.Name,
			"secretField", secretAccessKeyField,
			"keyInfo", keyInfoForLog(keyInfo),
		)
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
	key.Status.ObservedGeneration = key.GetGeneration()
	key.Status.LastSyncTime = &now
	meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "KeyCreated",
		Message:            "Garage key created",
		ObservedGeneration: key.Status.ObservedGeneration,
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, key, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(key, nil, corev1.EventTypeNormal, "KeyCreated", "CreateKey", "Garage key created with access key id %s", accessKeyID)
	log.Info("Created Garage key", "namespace", key.Namespace, "name", key.Name, "accessKeyID", accessKeyID)
	if specChanged {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

func (r *GarageKeyReconciler) reconcileUpdate(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	zone string,
	s3EndpointURL string,
	refreshInterval time.Duration,
	specChanged bool,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	accessKeyID := strings.TrimSpace(key.Status.AccessKeyID)
	if accessKeyID == "" {
		err := fmt.Errorf("status.accessKeyId is required")
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	secret, secretExists, err := r.getCredentialsSecret(ctx, key)
	if err != nil {
		if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
	}

	if secretExists {
		if err := ensureSecretHasAllFields(
			ctx,
			r.Client,
			secret,
			zone,
			s3EndpointURL,
			key.Status.AccessKeyID,
			key.Status.SecretAccessKey,
			key.Spec.BucketRef.Name,
		); err != nil {
			if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			log.Error(err, "Could not migrate credentials Secret fields", "namespace", key.Namespace, "name", key.Name, "secretName", computeSecretName(key))
			return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
		}
	}

	if !secretExists {
		if strings.TrimSpace(key.Status.SecretAccessKey) == "" {
			err := fmt.Errorf("garage key exists but status.secretAccessKey is empty")
			if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", err.Error()); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			log.Error(err, "Could not recover credentials Secret from status", "namespace", key.Namespace, "name", key.Name, "accessKeyID", accessKeyID)
			return ctrl.Result{RequeueAfter: keyRequeueDelay}, nil
		}

		secret, buildErr := buildSecretFromStatus(key, zone, s3EndpointURL, key.Spec.BucketRef.Name)
		if buildErr != nil {
			if statusErr := r.updateKeyStatusCondition(ctx, key, "UpdateFailed", buildErr.Error()); statusErr != nil {
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

		log.Info("Recovered credentials Secret from GarageKey status", "namespace", key.Namespace, "name", key.Name, "secretName", secret.Name)
	}

	before := key.DeepCopy()
	now := metav1.Now()
	key.Status.ObservedGeneration = key.GetGeneration()
	key.Status.LastSyncTime = &now
	meta.SetStatusCondition(&key.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "KeyReady",
		Message:            "Garage key is in desired state",
		ObservedGeneration: key.Status.ObservedGeneration,
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, key, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(key, nil, corev1.EventTypeNormal, "KeyReady", "SyncKey", "Garage key is in desired state")
	log.Info("Reconciled Garage key", "namespace", key.Namespace, "name", key.Name, "accessKeyID", accessKeyID)
	if specChanged {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: refreshInterval}, nil
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

	if strings.TrimSpace(key.Status.SecretAccessKey) != "" {
		beforeStatus := key.DeepCopy()
		key.Status.SecretAccessKey = ""
		if err := r.Status().Patch(ctx, key, client.MergeFrom(beforeStatus)); err != nil {
			return ctrl.Result{}, err
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
	bucketNamespace := key.Namespace
	if key.Spec.BucketRef.Namespace != nil {
		bucketNamespace = strings.TrimSpace(*key.Spec.BucketRef.Namespace)
		if bucketNamespace == "" {
			return nil, "", fmt.Errorf("spec.bucketRef.namespace cannot be empty")
		}
	}

	bucket := &storagev1alpha1.GarageBucket{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: bucketNamespace, Name: bucketName}, bucket); err != nil {
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

	return nil
}

func (r *GarageKeyReconciler) getCredentialsSecret(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
) (*corev1.Secret, bool, error) {
	secret := &corev1.Secret{}
	secretKey := types.NamespacedName{Namespace: key.Namespace, Name: computeSecretName(key)}
	if err := r.Get(ctx, secretKey, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return secret, true, nil
}

func (r *GarageKeyReconciler) persistKeyCredentials(
	ctx context.Context,
	key *storagev1alpha1.GarageKey,
	accessKeyID string,
	secretAccessKey string,
) error {
	accessKeyID = strings.TrimSpace(accessKeyID)
	secretAccessKey = strings.TrimSpace(secretAccessKey)
	if accessKeyID == "" || secretAccessKey == "" {
		return fmt.Errorf("access key id and secret access key are required")
	}
	if key.Status.AccessKeyID == accessKeyID && key.Status.SecretAccessKey == secretAccessKey {
		return nil
	}

	before := key.DeepCopy()
	key.Status.AccessKeyID = accessKeyID
	key.Status.SecretAccessKey = secretAccessKey
	return r.Status().Patch(ctx, key, client.MergeFrom(before))
}

func (r *GarageKeyReconciler) resolveStoreReadinessAndS3Config(
	ctx context.Context,
	namespace string,
	storeRef *storagev1alpha1.GarageStoreRef,
) (bool, string, string, error) {
	storeName, storeKind, storeNamespace, err := resolveGarageStoreRef(storeRef, namespace)
	if err != nil {
		return false, "", "", fmt.Errorf("invalid store reference: %w", err)
	}

	switch storeKind {
	case storagev1alpha1.GarageStoreRefKindGarageStore:
		nsStore := &storagev1alpha1.GarageStore{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: storeNamespace, Name: storeName}, nsStore); err != nil {
			return false, "", "", err
		}
		return nsStore.Status.Ready, nsStore.Spec.Zone, nsStore.Spec.S3EndpointURL, nil
	case storagev1alpha1.GarageStoreRefKindGarageClusterStore:
		clusterStore := &storagev1alpha1.GarageClusterStore{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: storeNamespace, Name: storeName}, clusterStore); err != nil {
			return false, "", "", err
		}
		return clusterStore.Status.Ready, clusterStore.Spec.Zone, clusterStore.Spec.S3EndpointURL, nil
	default:
		return false, "", "", fmt.Errorf("unsupported store reference kind %q", storeKind)
	}
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
	key.Status.ObservedGeneration = key.GetGeneration()

	return r.Status().Patch(ctx, key, client.MergeFrom(before))
}

func keyInfoForLog(keyInfo *garage.KeyInfo) map[string]any {
	if keyInfo == nil {
		return nil
	}

	return map[string]any{
		"id":                     keyInfo.ID,
		"name":                   keyInfo.Name,
		"accessKeyID":            keyInfo.AccessKeyID,
		"secretAccessKeyField":   keyInfo.SecretAccessKeyField(),
		"secretAccessKeyPresent": keyInfo.GetSecretAccessKey() != "",
		"metadata":               keyInfo.Metadata,
		"bucketPerms":            keyInfo.BucketPerms,
		"permissions":            keyInfo.Permissions,
		"buckets":                keyInfo.Buckets,
		"createdAt":              keyInfo.CreatedAt,
		"created":                keyInfo.Created,
		"expiration":             keyInfo.Expiration,
		"expired":                keyInfo.Expired,
	}
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
		if item.Spec.BucketRef.Namespace != nil {
			refNS := strings.TrimSpace(*item.Spec.BucketRef.Namespace)
			if refNS == "" || refNS != bucket.Namespace {
				continue
			}
		} else if item.Namespace != bucket.Namespace {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Namespace: item.Namespace, Name: item.Name},
		})
	}

	return requests
}
