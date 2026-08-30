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
	"net/url"
	"path"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	storagev1alpha1 "github.com/deuxfleurs-org/garage-provisioner-operator/api/v1alpha1"
	"github.com/deuxfleurs-org/garage-provisioner-operator/internal/garage"
)

func buildCreateKeyReq(key *storagev1alpha1.GarageKey, bucketID string) *garage.CreateKeyRequest {
	if key == nil {
		return nil
	}

	bucketName := strings.TrimSpace(key.Spec.BucketRef.Name)
	keyName := strings.TrimSpace(key.Name)
	name := keyName
	if bucketName != "" {
		name = fmt.Sprintf("%s-%s", bucketName, keyName)
	}

	metadata := map[string]string{}
	if strings.TrimSpace(bucketID) != "" {
		metadata["bucketId"] = strings.TrimSpace(bucketID)
	}

	return &garage.CreateKeyRequest{Name: name, Metadata: metadata}
}

func buildSecretFromKey(
	key *storagev1alpha1.GarageKey,
	keyInfo *garage.KeyInfo,
	storeURL string,
) *corev1.Secret {
	if key == nil || keyInfo == nil {
		return nil
	}

	secretAccessKey := keyInfo.SecretAccessKey
	if secretAccessKey == "" {
		secretAccessKey = keyInfo.SecretKey
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      computeSecretName(key),
			Namespace: key.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"AWS_ACCESS_KEY_ID":     []byte(keyInfo.AccessKeyID),
			"AWS_SECRET_ACCESS_KEY": []byte(secretAccessKey),
			"AWS_S3_ENDPOINT":       []byte(extractS3Endpoint(storeURL)),
			"AWS_REGION":            []byte("garage"),
			"bucket_name":           []byte(key.Spec.BucketRef.Name),
		},
	}

	return secret
}

func extractS3Endpoint(storeURL string) string {
	trimmed := strings.TrimSpace(storeURL)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return trimmed
	}

	cleanPath := parsed.Path
	for _, suffix := range []string{"/v2", "/api"} {
		if before, ok := strings.CutSuffix(cleanPath, suffix); ok {
			cleanPath = before
		}
	}
	cleanPath = strings.TrimRight(cleanPath, "/")
	if cleanPath == "" {
		parsed.Path = ""
	} else {
		parsed.Path = path.Clean(cleanPath)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""

	return parsed.String()
}

func computeSecretName(key *storagev1alpha1.GarageKey) string {
	if key == nil {
		return ""
	}

	if key.Spec.SecretRef != nil && strings.TrimSpace(key.Spec.SecretRef.Name) != "" {
		return key.Spec.SecretRef.Name
	}

	return fmt.Sprintf("%s-credentials", key.Name)
}

func updateKeyPermissions(
	ctx context.Context,
	garageClient *garage.GarageAdminClient,
	bucketID string,
	keyID string,
	desired storagev1alpha1.BucketPermissions,
	current storagev1alpha1.BucketPermissions,
) error {
	toAllow, toDeny := garage.CanonicalizePermissions(
		garage.BucketPermissions{Read: current.Read, Write: current.Write, Owner: current.Owner},
		garage.BucketPermissions{Read: desired.Read, Write: desired.Write, Owner: desired.Owner},
	)

	if toAllow.Read || toAllow.Write || toAllow.Owner {
		if err := garageClient.AllowBucketKey(ctx, bucketID, keyID, toAllow); err != nil {
			return err
		}
	}

	if toDeny.Read || toDeny.Write || toDeny.Owner {
		if err := garageClient.DenyBucketKey(ctx, bucketID, keyID, toDeny); err != nil {
			return err
		}
	}

	return nil
}
