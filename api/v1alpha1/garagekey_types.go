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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GarageKeySpec defines the desired state of GarageKey.
type GarageKeySpec struct {
	// bucketRef references the target GarageBucket.
	BucketRef GarageBucketReference `json:"bucketRef"`

	// bucketPermissions configures access for this key on the referenced bucket.
	BucketPermissions BucketPermissions `json:"bucketPermissions"`

	// secretRef selects where generated credentials are stored.
	// If omitted, the controller should use "{name}-credentials".
	// +optional
	SecretRef *LocalSecretReference `json:"secretRef,omitempty"`
}

// GarageKeyStatus defines the observed state of GarageKey.
type GarageKeyStatus struct {
	// accessKeyId is the Garage access key identifier.
	// +optional
	AccessKeyID string `json:"accessKeyId,omitempty"`

	// lastSyncTime is the last successful reconciliation timestamp.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// conditions represent the current state of the GarageKey resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GarageKey is the Schema for the garagekeys API
type GarageKey struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GarageKey
	// +required
	Spec GarageKeySpec `json:"spec"`

	// status defines the observed state of GarageKey
	// +optional
	Status GarageKeyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GarageKeyList contains a list of GarageKey
type GarageKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GarageKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GarageKey{}, &GarageKeyList{})
		return nil
	})
}
