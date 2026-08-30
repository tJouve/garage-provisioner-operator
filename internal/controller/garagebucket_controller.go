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
	"errors"
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

// GarageBucketReconciler reconciles a GarageBucket object.
type GarageBucketReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

const (
	garageBucketFinalizer = "garagebucket.storage.deuxfleurs.fr/finalizer"
	bucketRequeueDelay    = 30 * time.Second
)

// +kubebuilder:rbac:groups=storage.deuxfleurs.fr,resources=garagebuckets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.deuxfleurs.fr,resources=garagebuckets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=storage.deuxfleurs.fr,resources=garagebuckets/finalizers,verbs=update
// +kubebuilder:rbac:groups=storage.deuxfleurs.fr,resources=garagestores,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.deuxfleurs.fr,resources=garageclusterstores,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop.
func (r *GarageBucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	bucket := &storagev1alpha1.GarageBucket{}
	if err := r.Get(ctx, req.NamespacedName, bucket); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	storeReady, err := r.isStoreReady(ctx, bucket)
	if err != nil {
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "StoreNotReady", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not resolve store reference", "namespace", bucket.Namespace, "name", bucket.Name)
		r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "StoreNotReady", "ResolveStore", "%s", err.Error())
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}
	if !storeReady {
		message := "Referenced store is not ready"
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "StoreNotReady", message); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Info("Referenced store is not ready", "namespace", bucket.Namespace, "name", bucket.Name, "storeRef", bucket.Spec.ClusterRef.Name)
		r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "StoreNotReady", "StoreReady", "%s", message)
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	garageClient, err := LoadGarageClientFromStore(ctx, r.Client, &bucket.Spec.ClusterRef, bucket.Namespace)
	if err != nil {
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not load Garage admin client", "namespace", bucket.Namespace, "name", bucket.Name)
		r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "UpdateFailed", "LoadClient", "%s", err.Error())
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	if !bucket.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, bucket, garageClient)
	}

	if !controllerutil.ContainsFinalizer(bucket, garageBucketFinalizer) {
		before := bucket.DeepCopy()
		controllerutil.AddFinalizer(bucket, garageBucketFinalizer)
		if err := r.Patch(ctx, bucket, client.MergeFrom(before)); err != nil {
			return ctrl.Result{}, err
		}
	}

	if strings.TrimSpace(bucket.Status.BucketID) == "" {
		return r.reconcileCreate(ctx, bucket, garageClient)
	}

	return r.reconcileUpdate(ctx, bucket, garageClient)
}

func (r *GarageBucketReconciler) reconcileCreate(
	ctx context.Context,
	bucket *storagev1alpha1.GarageBucket,
	garageClient *garage.GarageAdminClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	quotas, err := convertQuotasToGarageFormat(bucket.Spec.Quotas)
	if err != nil {
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	createReq := buildCreateBucketReq(bucket)
	if createReq == nil {
		err := fmt.Errorf("could not build bucket create request")
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}
	createReq.Quotas = quotas

	bucketInfo, err := garageClient.CreateBucket(ctx, *createReq)
	if err != nil {
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not create Garage bucket", "namespace", bucket.Namespace, "name", bucket.Name)
		r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "CreateFailed", "CreateBucket", "%s", err.Error())
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	bucketID := strings.TrimSpace(bucketInfo.BucketID)
	if bucketID == "" {
		bucketID = strings.TrimSpace(bucketInfo.ID)
	}
	if bucketID == "" {
		err := fmt.Errorf("garage create bucket response did not include bucket identifier")
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "CreateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	before := bucket.DeepCopy()
	now := metav1.Now()
	bucket.Status.BucketID = bucketID
	bucket.Status.LastSyncTime = &now
	meta.SetStatusCondition(&bucket.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "BucketCreated",
		Message:            "Garage bucket created",
		ObservedGeneration: bucket.GetGeneration(),
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, bucket, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(bucket, nil, corev1.EventTypeNormal, "BucketCreated", "CreateBucket", "Garage bucket created with id %s", bucketID)
	log.Info("Created Garage bucket", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucketID)
	return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
}

func (r *GarageBucketReconciler) reconcileUpdate(
	ctx context.Context,
	bucket *storagev1alpha1.GarageBucket,
	garageClient *garage.GarageAdminClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	bucketInfo, err := garageClient.GetBucketInfo(ctx, bucket.Status.BucketID)
	if err != nil {
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		log.Error(err, "Could not get Garage bucket info", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucket.Status.BucketID)
		r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "UpdateFailed", "GetBucketInfo", "%s", err.Error())
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	desiredQuotas, err := convertQuotasToGarageFormat(bucket.Spec.Quotas)
	if err != nil {
		if statusErr := r.updateBucketStatusCondition(ctx, bucket, "UpdateFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
	}

	needsUpdate := !bucketQuotasEqual(desiredQuotas, bucketInfo.Quotas)
	if needsUpdate {
		updateReq := garage.UpdateBucketReq{Quotas: desiredQuotas}
		if _, err := garageClient.UpdateBucket(ctx, bucket.Status.BucketID, updateReq); err != nil {
			if statusErr := r.updateBucketStatusCondition(ctx, bucket, "UpdateFailed", err.Error()); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			log.Error(err, "Could not update Garage bucket", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucket.Status.BucketID)
			r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "UpdateFailed", "UpdateBucket", "%s", err.Error())
			return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
		}
	}

	before := bucket.DeepCopy()
	now := metav1.Now()
	bucket.Status.LastSyncTime = &now
	meta.SetStatusCondition(&bucket.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionTrue,
		Reason:             "BucketReady",
		Message:            "Garage bucket is in desired state",
		ObservedGeneration: bucket.GetGeneration(),
		LastTransitionTime: now,
	})
	if err := r.Status().Patch(ctx, bucket, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciled Garage bucket", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucket.Status.BucketID, "updated", needsUpdate)
	r.Recorder.Eventf(bucket, nil, corev1.EventTypeNormal, "BucketReady", "SyncBucket", "Garage bucket is in desired state")
	return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
}

func (r *GarageBucketReconciler) reconcileDelete(
	ctx context.Context,
	bucket *storagev1alpha1.GarageBucket,
	garageClient *garage.GarageAdminClient,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(bucket, garageBucketFinalizer) {
		return ctrl.Result{}, nil
	}

	policy := bucket.Spec.DeletionPolicy
	if policy == "" {
		policy = storagev1alpha1.GarageDeletionPolicyDelete
	}

	if policy == storagev1alpha1.GarageDeletionPolicyDelete && strings.TrimSpace(bucket.Status.BucketID) != "" {
		bucketInfo, err := garageClient.GetBucketInfo(ctx, bucket.Status.BucketID)
		if err != nil {
			if isGarageNotFound(err) {
				bucketInfo = nil
			} else {
				if statusErr := r.updateBucketStatusCondition(ctx, bucket, "DeleteFailed", err.Error()); statusErr != nil {
					return ctrl.Result{}, statusErr
				}
				log.Error(err, "Could not get Garage bucket info before delete", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucket.Status.BucketID)
				r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "DeleteFailed", "GetBucketInfo", "%s", err.Error())
				return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
			}
		}

		if bucketInfo != nil && !isBucketEmpty(bucketInfo) {
			message := "waiting for objects to be deleted"
			if statusErr := r.updateBucketStatusCondition(ctx, bucket, "BucketNotEmpty", message); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			log.Info("Garage bucket is not empty", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucket.Status.BucketID)
			r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "BucketNotEmpty", "DeleteBucket", "%s", message)
			return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
		}

		if err := garageClient.DeleteBucket(ctx, bucket.Status.BucketID); err != nil {
			if statusErr := r.updateBucketStatusCondition(ctx, bucket, "DeleteFailed", err.Error()); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			log.Error(err, "Could not delete Garage bucket", "namespace", bucket.Namespace, "name", bucket.Name, "bucketID", bucket.Status.BucketID)
			r.Recorder.Eventf(bucket, nil, corev1.EventTypeWarning, "DeleteFailed", "DeleteBucket", "%s", err.Error())
			return ctrl.Result{RequeueAfter: bucketRequeueDelay}, nil
		}
	}

	before := bucket.DeepCopy()
	controllerutil.RemoveFinalizer(bucket, garageBucketFinalizer)
	if err := r.Patch(ctx, bucket, client.MergeFrom(before)); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Removed GarageBucket finalizer", "namespace", bucket.Namespace, "name", bucket.Name)
	return ctrl.Result{}, nil
}

func (r *GarageBucketReconciler) isStoreReady(ctx context.Context, bucket *storagev1alpha1.GarageBucket) (bool, error) {
	storeName := strings.TrimSpace(bucket.Spec.ClusterRef.Name)
	if storeName == "" {
		return false, fmt.Errorf("spec.clusterRef.name is required")
	}

	nsStore := &storagev1alpha1.GarageStore{}
	err := r.Get(ctx, client.ObjectKey{Namespace: bucket.Namespace, Name: storeName}, nsStore)
	if err == nil {
		return nsStore.Status.Ready, nil
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("could not get GarageStore %s/%s: %w", bucket.Namespace, storeName, err)
	}

	clusterStore := &storagev1alpha1.GarageClusterStore{}
	if err := r.Get(ctx, client.ObjectKey{Name: storeName}, clusterStore); err != nil {
		return false, fmt.Errorf("could not get GarageClusterStore %s: %w", storeName, err)
	}

	return clusterStore.Status.Ready, nil
}

func (r *GarageBucketReconciler) updateBucketStatusCondition(
	ctx context.Context,
	bucket *storagev1alpha1.GarageBucket,
	reason string,
	message string,
) error {
	before := bucket.DeepCopy()
	now := metav1.Now()

	meta.SetStatusCondition(&bucket.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: bucket.GetGeneration(),
		LastTransitionTime: now,
	})

	return r.Status().Patch(ctx, bucket, client.MergeFrom(before))
}

func bucketQuotasEqual(a, b *garage.BucketQuotas) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	if !int64PtrEqual(a.MaxSize, b.MaxSize) {
		return false
	}
	if !int64PtrEqual(a.MaxObjects, b.MaxObjects) {
		return false
	}

	return true
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func isGarageNotFound(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *garage.GarageAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 404
	}

	return false
}

// SetupWithManager sets up the controller with the Manager.
func (r *GarageBucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("garagebucket-controller")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.GarageBucket{}).
		Watches(
			&storagev1alpha1.GarageStore{},
			handler.EnqueueRequestsFromMapFunc(r.mapGarageStoreToBuckets),
		).
		Watches(
			&storagev1alpha1.GarageClusterStore{},
			handler.EnqueueRequestsFromMapFunc(r.mapGarageClusterStoreToBuckets),
		).
		Named("garagebucket").
		Complete(r)
}

func (r *GarageBucketReconciler) mapGarageStoreToBuckets(ctx context.Context, obj client.Object) []reconcile.Request {
	store, ok := obj.(*storagev1alpha1.GarageStore)
	if !ok {
		return nil
	}

	bucketList := &storagev1alpha1.GarageBucketList{}
	if err := r.List(ctx, bucketList, client.InNamespace(store.Namespace)); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range bucketList.Items {
		bucket := &bucketList.Items[i]
		if bucket.Spec.ClusterRef.Name != store.Name {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: bucket.Namespace, Name: bucket.Name}})
	}

	return requests
}

func (r *GarageBucketReconciler) mapGarageClusterStoreToBuckets(ctx context.Context, obj client.Object) []reconcile.Request {
	store, ok := obj.(*storagev1alpha1.GarageClusterStore)
	if !ok {
		return nil
	}

	bucketList := &storagev1alpha1.GarageBucketList{}
	if err := r.List(ctx, bucketList); err != nil {
		return nil
	}

	requests := make([]reconcile.Request, 0)
	for i := range bucketList.Items {
		bucket := &bucketList.Items[i]
		if bucket.Spec.ClusterRef.Name != store.Name {
			continue
		}
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: bucket.Namespace, Name: bucket.Name}})
	}

	return requests
}
