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

// SecretKeyRef points to a key in a Kubernetes Secret.
type SecretKeyRef struct {
	// name is the Secret name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key is the Secret data key.
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// GarageStoreReference references a Garage store object by name.
type GarageStoreReference struct {
	// name references a GarageClusterStore or GarageStore object.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// GarageBucketReference references a GarageBucket object by name.
type GarageBucketReference struct {
	// name references a GarageBucket object.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// LocalSecretReference references a Secret by name in the same namespace.
type LocalSecretReference struct {
	// name is the Secret name.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// GarageBucketQuotas defines optional bucket quota settings.
type GarageBucketQuotas struct {
	// maxSize is the maximum bucket size in bytes.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxSize *int64 `json:"maxSize,omitempty"`

	// maxObjects is the maximum number of objects.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxObjects *int64 `json:"maxObjects,omitempty"`
}

// BucketPermissions defines bucket-level permissions for a key.
type BucketPermissions struct {
	// read grants object read access.
	// +optional
	Read bool `json:"read,omitempty"`

	// write grants object write access.
	// +optional
	Write bool `json:"write,omitempty"`

	// owner grants owner-level access.
	// +optional
	Owner bool `json:"owner,omitempty"`
}

// GarageDeletionPolicy defines deletion behavior for external resources.
// +kubebuilder:validation:Enum=Delete;Retain
type GarageDeletionPolicy string

const (
	// GarageDeletionPolicyDelete deletes external Garage resources when Kubernetes resources are deleted.
	GarageDeletionPolicyDelete GarageDeletionPolicy = "Delete"
	// GarageDeletionPolicyRetain keeps external Garage resources when Kubernetes resources are deleted.
	GarageDeletionPolicyRetain GarageDeletionPolicy = "Retain"
)
