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
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	storagev1alpha1 "github.com/tJouve/garage-provisioner-operator/api/v1alpha1"
	"github.com/tJouve/garage-provisioner-operator/internal/garage"
)

const (
	credentialsSecretKeyZone            = "zone"
	credentialsSecretKeyS3EndpointURL   = "s3_endpoint_url"
	credentialsSecretKeyAccessKeyID     = "access_key_id"
	credentialsSecretKeySecretAccessKey = "secret_access_key"
	credentialsSecretKeyBucketName      = "bucket_name"
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
	zone string,
	s3EndpointURL string,
	bucketName string,
) (*corev1.Secret, error) {
	if key == nil || keyInfo == nil {
		return nil, fmt.Errorf("garage key and key info are required")
	}

	secretData, err := buildCredentialsSecretData(zone, s3EndpointURL, keyInfo.AccessKeyID, keyInfo.GetSecretAccessKey(), bucketName)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      computeSecretName(key),
			Namespace: key.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: secretData,
	}

	return secret, nil
}

func buildSecretFromStatus(
	key *storagev1alpha1.GarageKey,
	zone string,
	s3EndpointURL string,
	bucketName string,
) (*corev1.Secret, error) {
	if key == nil {
		return nil, fmt.Errorf("garage key is required")
	}

	secretData, err := buildCredentialsSecretData(zone, s3EndpointURL, key.Status.AccessKeyID, key.Status.SecretAccessKey, bucketName)
	if err != nil {
		return nil, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      computeSecretName(key),
			Namespace: key.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: secretData,
	}

	return secret, nil
}

func buildCredentialsSecretData(zone, s3EndpointURL, accessKeyID, secretAccessKey, bucketName string) (map[string][]byte, error) {
	secretData := map[string][]byte{
		credentialsSecretKeyZone:            []byte(strings.TrimSpace(zone)),
		credentialsSecretKeyS3EndpointURL:   []byte(strings.TrimSpace(s3EndpointURL)),
		credentialsSecretKeyAccessKeyID:     []byte(strings.TrimSpace(accessKeyID)),
		credentialsSecretKeySecretAccessKey: []byte(strings.TrimSpace(secretAccessKey)),
		credentialsSecretKeyBucketName:      []byte(strings.TrimSpace(bucketName)),
	}

	if err := validateCredentialsSecretData(secretData); err != nil {
		return nil, err
	}

	return secretData, nil
}

func ensureSecretHasAllFields(
	ctx context.Context,
	r client.Client,
	secret *corev1.Secret,
	zone string,
	s3EndpointURL string,
	accessKeyID string,
	secretAccessKey string,
	bucketName string,
) error {
	if secret == nil {
		return fmt.Errorf("secret is required")
	}
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}

	requiredFields := []string{
		credentialsSecretKeyZone,
		credentialsSecretKeyS3EndpointURL,
		credentialsSecretKeyAccessKeyID,
		credentialsSecretKeySecretAccessKey,
		credentialsSecretKeyBucketName,
	}

	needsUpdate := false
	for _, field := range requiredFields {
		value, exists := secret.Data[field]
		if !exists || strings.TrimSpace(string(value)) == "" {
			needsUpdate = true
			break
		}
	}

	if !needsUpdate {
		return nil
	}

	newData, err := buildCredentialsSecretData(
		firstNonEmpty(zone, string(secret.Data[credentialsSecretKeyZone])),
		firstNonEmpty(s3EndpointURL, string(secret.Data[credentialsSecretKeyS3EndpointURL])),
		firstNonEmpty(accessKeyID, string(secret.Data[credentialsSecretKeyAccessKeyID])),
		firstNonEmpty(secretAccessKey, string(secret.Data[credentialsSecretKeySecretAccessKey])),
		firstNonEmpty(bucketName, string(secret.Data[credentialsSecretKeyBucketName])),
	)
	if err != nil {
		return err
	}

	maps.Copy(secret.Data, newData)

	if err := r.Update(ctx, secret); err != nil {
		return fmt.Errorf("failed to patch Secret with missing fields: %w", err)
	}

	return nil
}

func firstNonEmpty(primary string, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}

	return fallback
}

func validateCredentialsSecretData(data map[string][]byte) error {
	requiredKeys := []string{
		credentialsSecretKeyZone,
		credentialsSecretKeyS3EndpointURL,
		credentialsSecretKeyAccessKeyID,
		credentialsSecretKeySecretAccessKey,
		credentialsSecretKeyBucketName,
	}

	for _, key := range requiredKeys {
		value, ok := data[key]
		if !ok || strings.TrimSpace(string(value)) == "" {
			return fmt.Errorf("credentials Secret requires non-empty %q", key)
		}
	}

	return nil
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
	log := logf.FromContext(ctx)

	log.Info("Entered updateKeyPermissions", "bucketID", bucketID, "keyID", keyID)

	log.Info(
		"Updating key permissions",
		"bucketID", bucketID,
		"keyID", keyID,
		"desired", fmt.Sprintf("read=%t write=%t owner=%t", desired.Read, desired.Write, desired.Owner),
		"current", fmt.Sprintf("read=%t write=%t owner=%t", current.Read, current.Write, current.Owner),
	)

	toAllow, toDeny := garage.CanonicalizePermissions(
		garage.BucketPermissions{Read: current.Read, Write: current.Write, Owner: current.Owner},
		garage.BucketPermissions{Read: desired.Read, Write: desired.Write, Owner: desired.Owner},
	)

	log.Info(
		"Canonicalized key permissions",
		"bucketID", bucketID,
		"keyID", keyID,
		"toAllow", fmt.Sprintf("read=%t write=%t owner=%t", toAllow.Read, toAllow.Write, toAllow.Owner),
		"toDeny", fmt.Sprintf("read=%t write=%t owner=%t", toDeny.Read, toDeny.Write, toDeny.Owner),
	)

	if toAllow.Read || toAllow.Write || toAllow.Owner {
		log.Info(
			"Calling AllowBucketKey",
			"bucketID", bucketID,
			"keyID", keyID,
			"permissions", fmt.Sprintf("read=%t write=%t owner=%t", toAllow.Read, toAllow.Write, toAllow.Owner),
		)
		if err := garageClient.AllowBucketKey(ctx, bucketID, keyID, toAllow); err != nil {
			log.Error(err, "AllowBucketKey failed", "bucketID", bucketID, "keyID", keyID)
			return err
		}
		log.Info("AllowBucketKey succeeded", "bucketID", bucketID, "keyID", keyID)
	}

	if toDeny.Read || toDeny.Write || toDeny.Owner {
		log.Info(
			"Calling DenyBucketKey",
			"bucketID", bucketID,
			"keyID", keyID,
			"permissions", fmt.Sprintf("read=%t write=%t owner=%t", toDeny.Read, toDeny.Write, toDeny.Owner),
		)
		if err := garageClient.DenyBucketKey(ctx, bucketID, keyID, toDeny); err != nil {
			log.Error(err, "DenyBucketKey failed", "bucketID", bucketID, "keyID", keyID)
			return err
		}
		log.Info("DenyBucketKey succeeded", "bucketID", bucketID, "keyID", keyID)
	}

	log.Info("Finished updating key permissions", "bucketID", bucketID, "keyID", keyID)

	return nil
}
