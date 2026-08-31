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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	storagev1alpha1 "github.com/tJouve/garage-provisioner-operator/api/v1alpha1"
)

// GarageStoreReconciler reconciles a GarageStore object
type GarageStoreReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=smokingcat.net,resources=garagestores,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=smokingcat.net,resources=garagestores/status,verbs=update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get

func (r *GarageStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	store := &storagev1alpha1.GarageStore{}
	if err := r.Get(ctx, req.NamespacedName, store); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	specChanged := store.GetGeneration() != store.Status.ObservedGeneration
	refreshInterval := getRefreshInterval(store.Spec.RefreshInterval)

	garageClient, err := loadGarageClientFromSpec(ctx, r.Client, store.Namespace, store.Spec.AdminURL, store.Spec.AdminSecretRef)
	if err != nil {
		if statusErr := UpdateStoreStatus(ctx, r.Client, store, false, err); statusErr != nil {
			return ctrl.Result{}, statusErr
		}

		log.Error(err, "Could not load Garage admin client", "namespace", store.Namespace, "name", store.Name)
		r.Recorder.Eventf(store, nil, corev1.EventTypeWarning, reasonHealthCheckFailed, "LoadClient", "%s", err.Error())
		return ctrl.Result{RequeueAfter: transientRequeueDelay}, nil
	}

	if err := garageClient.Health(ctx); err != nil {
		if statusErr := UpdateStoreStatus(ctx, r.Client, store, false, err); statusErr != nil {
			return ctrl.Result{}, statusErr
		}

		requeueDelay := transientRequeueDelay
		if isAuthError(err) {
			requeueDelay = authRequeueDelay
		}

		log.Error(err, "Could not validate Garage admin endpoint", "namespace", store.Namespace, "name", store.Name)
		r.Recorder.Eventf(store, nil, corev1.EventTypeWarning, reasonHealthCheckFailed, "HealthCheck", "%s", err.Error())
		return ctrl.Result{RequeueAfter: requeueDelay}, nil
	}

	if err := UpdateStoreStatus(ctx, r.Client, store, true, nil); err != nil {
		return ctrl.Result{}, err
	}

	r.Recorder.Eventf(store, nil, corev1.EventTypeNormal, reasonHealthCheckPassed, "HealthCheck", "Garage admin endpoint validated")
	log.Info("Validated Garage admin endpoint", "namespace", store.Namespace, "name", store.Name)
	if specChanged {
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: refreshInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GarageStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Recorder == nil {
		r.Recorder = mgr.GetEventRecorder("garagestore-controller")
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.GarageStore{}).
		Named("garagestore").
		Complete(r)
}
