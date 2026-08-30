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
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1alpha1 "github.com/deuxfleurs-org/garage-provisioner-operator/api/v1alpha1"
	"github.com/deuxfleurs-org/garage-provisioner-operator/internal/garage"
)

const (
	readyConditionType = "Ready"
	operatorNamespace  = "operators"

	reasonHealthCheckPassed = "HealthCheckPassed"
	reasonHealthCheckFailed = "HealthCheckFailed"
	reasonSecretNotFound    = "SecretNotFound"
	reasonInvalidURL        = "InvalidURL"
)

var (
	errSecretNotFound = errors.New("secret not found")
	errInvalidURL     = errors.New("invalid URL")
)

// LoadGarageClientFromStore resolves a GarageStore or GarageClusterStore and returns a configured Garage API client.
func LoadGarageClientFromStore(
	ctx context.Context,
	k8sClient client.Client,
	storeRef *storagev1alpha1.GarageStoreReference,
	namespace string,
) (*garage.GarageAdminClient, error) {
	if storeRef == nil {
		return nil, fmt.Errorf("store reference is required")
	}
	storeName := strings.TrimSpace(storeRef.Name)
	if storeName == "" {
		return nil, fmt.Errorf("store reference name is required")
	}

	if namespace != "" {
		nsStore := &storagev1alpha1.GarageStore{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: storeName}, nsStore); err == nil {
			return loadGarageClientFromSpec(ctx, k8sClient, nsStore.Spec.AdminURL, nsStore.Spec.AdminToken)
		} else if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("could not get GarageStore %s/%s: %w", namespace, storeName, err)
		}
	}

	clusterStore := &storagev1alpha1.GarageClusterStore{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Name: storeName}, clusterStore); err != nil {
		return nil, fmt.Errorf("could not get GarageClusterStore %s: %w", storeName, err)
	}

	return loadGarageClientFromSpec(ctx, k8sClient, clusterStore.Spec.AdminURL, clusterStore.Spec.AdminToken)
}

func loadGarageClientFromSpec(
	ctx context.Context,
	k8sClient client.Client,
	adminURL string,
	adminTokenRef storagev1alpha1.SecretKeyRef,
) (*garage.GarageAdminClient, error) {
	if _, err := url.ParseRequestURI(strings.TrimSpace(adminURL)); err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidURL, err)
	}

	secretName := strings.TrimSpace(adminTokenRef.Name)
	secretKey := strings.TrimSpace(adminTokenRef.Key)
	if secretName == "" || secretKey == "" {
		return nil, fmt.Errorf("%w: admin token Secret name and key are required", errSecretNotFound)
	}

	secret := &corev1.Secret{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: operatorNamespace, Name: secretName}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("%w: secret %s/%s not found", errSecretNotFound, operatorNamespace, secretName)
		}
		return nil, fmt.Errorf("could not get Secret %s/%s: %w", operatorNamespace, secretName, err)
	}

	tokenBytes, found := secret.Data[secretKey]
	if !found || len(tokenBytes) == 0 {
		return nil, fmt.Errorf("%w: secret %s/%s key %q not found or empty", errSecretNotFound, operatorNamespace, secretName, secretKey)
	}

	return garage.NewGarageAdminClient(adminURL, string(tokenBytes), nil), nil
}

// UpdateStoreStatus updates status for GarageClusterStore and GarageStore resources.
func UpdateStoreStatus(ctx context.Context, k8sClient client.Client, store any, ready bool, reconcileErr error) error {
	switch typedStore := store.(type) {
	case *storagev1alpha1.GarageClusterStore:
		return updateGarageClusterStoreStatus(ctx, k8sClient, typedStore, ready, reconcileErr)
	case *storagev1alpha1.GarageStore:
		return updateGarageStoreStatus(ctx, k8sClient, typedStore, ready, reconcileErr)
	default:
		return fmt.Errorf("unsupported store type %T", store)
	}
}

func updateGarageClusterStoreStatus(
	ctx context.Context,
	k8sClient client.Client,
	store *storagev1alpha1.GarageClusterStore,
	ready bool,
	reconcileErr error,
) error {
	before := store.DeepCopy()
	reason, message, status := readyConditionFields(ready, reconcileErr)

	store.Status.Ready = ready
	if ready {
		now := metav1.Now()
		store.Status.LastValidation = &now
	}
	meta.SetStatusCondition(&store.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: store.GetGeneration(),
		LastTransitionTime: metav1.Now(),
	})

	return k8sClient.Status().Patch(ctx, store, client.MergeFrom(before))
}

func updateGarageStoreStatus(
	ctx context.Context,
	k8sClient client.Client,
	store *storagev1alpha1.GarageStore,
	ready bool,
	reconcileErr error,
) error {
	before := store.DeepCopy()
	reason, message, status := readyConditionFields(ready, reconcileErr)

	store.Status.Ready = ready
	if ready {
		now := metav1.Now()
		store.Status.LastValidation = &now
	}
	meta.SetStatusCondition(&store.Status.Conditions, metav1.Condition{
		Type:               readyConditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: store.GetGeneration(),
		LastTransitionTime: metav1.Now(),
	})

	return k8sClient.Status().Patch(ctx, store, client.MergeFrom(before))
}

func readyConditionFields(ready bool, reconcileErr error) (string, string, metav1.ConditionStatus) {
	if ready {
		return reasonHealthCheckPassed, "Garage admin endpoint validated", metav1.ConditionTrue
	}

	reason := reasonHealthCheckFailed
	switch {
	case errors.Is(reconcileErr, errSecretNotFound) || apierrors.IsNotFound(reconcileErr):
		reason = reasonSecretNotFound
	case errors.Is(reconcileErr, errInvalidURL):
		reason = reasonInvalidURL
	}

	message := "Health check failed"
	if reconcileErr != nil {
		message = reconcileErr.Error()
	}

	return reason, message, metav1.ConditionFalse
}

func isAuthError(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *garage.GarageAPIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden
	}

	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden")
}
