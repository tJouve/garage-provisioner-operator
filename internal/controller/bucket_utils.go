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
	"fmt"

	storagev1alpha1 "github.com/deuxfleurs-org/garage-provisioner-operator/api/v1alpha1"
	"github.com/deuxfleurs-org/garage-provisioner-operator/internal/garage"
)

func convertQuotasToGarageFormat(spec *storagev1alpha1.GarageBucketQuotas) (*garage.BucketQuotas, error) {
	if spec == nil {
		return nil, nil
	}

	if spec.MaxSize != nil && *spec.MaxSize < 0 {
		return nil, fmt.Errorf("maxSize cannot be negative")
	}
	if spec.MaxObjects != nil && *spec.MaxObjects < 0 {
		return nil, fmt.Errorf("maxObjects cannot be negative")
	}

	return &garage.BucketQuotas{
		MaxSize:    spec.MaxSize,
		MaxObjects: spec.MaxObjects,
	}, nil
}

func isBucketEmpty(bucketInfo *garage.BucketInfo) bool {
	if bucketInfo == nil {
		return true
	}

	objectCount := firstNonNilInt64(bucketInfo.ObjectCount, bucketInfo.ObjectsCount, bucketInfo.Objects)
	uploadCount := firstNonNilInt64(bucketInfo.UploadCount, bucketInfo.UploadsCount, bucketInfo.Uploads)

	return objectCount == 0 && uploadCount == 0
}

func buildCreateBucketReq(bucket *storagev1alpha1.GarageBucket) *garage.CreateBucketRequest {
	if bucket == nil {
		return nil
	}

	request := &garage.CreateBucketRequest{
		Name: bucket.Name,
	}

	if bucket.Spec.GlobalAlias != nil {
		request.GlobalAlias = *bucket.Spec.GlobalAlias
	}

	// localAlias intentionally omitted for now.
	request.LocalAlias = nil

	return request
}

func firstNonNilInt64(values ...*int64) int64 {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return 0
}
