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

// GarageStoreSpec defines the desired state of GarageStore.
type GarageStoreSpec struct {
	// adminURL is the Garage admin API endpoint URL.
	// +kubebuilder:validation:Format=uri
	// +kubebuilder:validation:MinLength=1
	AdminURL string `json:"adminURL"`

	// adminToken references the Secret key containing the Garage admin token.
	AdminToken SecretKeyRef `json:"adminToken"`
}

// GarageStoreStatus defines the observed state of GarageStore.
type GarageStoreStatus struct {
	// ready indicates whether admin endpoint validation succeeded.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// lastValidation is the last successful validation timestamp.
	// +optional
	LastValidation *metav1.Time `json:"lastValidation,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GarageStore is the Schema for the garagestores API
type GarageStore struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GarageStore
	// +required
	Spec GarageStoreSpec `json:"spec"`

	// status defines the observed state of GarageStore
	// +optional
	Status GarageStoreStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GarageStoreList contains a list of GarageStore
type GarageStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GarageStore `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GarageStore{}, &GarageStoreList{})
		return nil
	})
}
