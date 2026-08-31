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

package garage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const defaultHTTPTimeout = 30 * time.Second

// GarageAdminClient wraps HTTP calls to the Garage v2 admin API.
type GarageAdminClient struct {
	baseURL    string
	adminToken string
	httpClient *http.Client
}

// CreateBucketReq defines bucket creation payload.
type CreateBucketReq struct {
	Name        string            `json:"name,omitempty"`
	GlobalAlias string            `json:"globalAlias,omitempty"`
	LocalAlias  *string           `json:"localAlias,omitempty"`
	Quotas      *BucketQuotas     `json:"quotas,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// CreateBucketRequest is kept as an alias for compatibility with controller helpers.
type CreateBucketRequest = CreateBucketReq

// UpdateBucketReq defines bucket update payload.
type UpdateBucketReq struct {
	GlobalAlias *string           `json:"globalAlias,omitempty"`
	Quotas      *BucketQuotas     `json:"quotas,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// BucketQuotas defines bucket quota values.
type BucketQuotas struct {
	MaxSize    *int64 `json:"maxSize,omitempty"`
	MaxObjects *int64 `json:"maxObjects,omitempty"`
}

// BucketInfo defines bucket information returned by Garage.
type BucketInfo struct {
	ID             string            `json:"id,omitempty"`
	BucketID       string            `json:"bucketId,omitempty"`
	Name           string            `json:"name,omitempty"`
	GlobalAlias    string            `json:"globalAlias,omitempty"`
	Quotas         *BucketQuotas     `json:"quotas,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ObjectCount    *int64            `json:"objectCount,omitempty"`
	ObjectsCount   *int64            `json:"objectsCount,omitempty"`
	Objects        *int64            `json:"objects,omitempty"`
	UploadCount    *int64            `json:"uploadCount,omitempty"`
	UploadsCount   *int64            `json:"uploadsCount,omitempty"`
	Uploads        *int64            `json:"uploads,omitempty"`
	IncompleteSize *int64            `json:"incompleteSize,omitempty"`
	CreatedAt      *time.Time        `json:"createdAt,omitempty"`
}

// CreateKeyReq defines key creation payload.
type CreateKeyReq struct {
	Name     string            `json:"name,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// CreateKeyRequest is kept as an alias for compatibility with controller helpers.
type CreateKeyRequest = CreateKeyReq

// KeyInfoBucket captures key permissions for one bucket.
type KeyInfoBucket struct {
	ID            string            `json:"id,omitempty"`
	GlobalAliases []string          `json:"globalAliases,omitempty"`
	LocalAliases  []string          `json:"localAliases,omitempty"`
	Permissions   BucketPermissions `json:"permissions,omitempty"`

	// Legacy compatibility fields.
	Read  bool `json:"read,omitempty"`
	Write bool `json:"write,omitempty"`
	Owner bool `json:"owner,omitempty"`
}

// KeyPerm defines key-level permissions.
type KeyPerm struct {
	CreateBucket bool `json:"createBucket,omitempty"`
}

// KeyInfo defines key information returned by Garage.
type KeyInfo struct {
	ID              string                       `json:"id,omitempty"`
	Name            string                       `json:"name,omitempty"`
	AccessKeyID     string                       `json:"accessKeyId,omitempty"`
	SecretKey       string                       `json:"secretKey,omitempty"`
	SecretAccessKey string                       `json:"secretAccessKey,omitempty"`
	Metadata        map[string]string            `json:"metadata,omitempty"`
	BucketPerms     map[string]BucketPermissions `json:"bucketPermissions,omitempty"`
	Permissions     *KeyPerm                     `json:"permissions,omitempty"`
	Buckets         []KeyInfoBucket              `json:"buckets,omitempty"`
	CreatedAt       *time.Time                   `json:"createdAt,omitempty"`
	Created         *time.Time                   `json:"created,omitempty"`
	Expiration      *time.Time                   `json:"expiration,omitempty"`
	Expired         bool                         `json:"expired,omitempty"`
}

// GetSecretAccessKey returns the secret access key from either Garage field variant.
func (k *KeyInfo) GetSecretAccessKey() string {
	if k == nil {
		return ""
	}

	if strings.TrimSpace(k.SecretAccessKey) != "" {
		return strings.TrimSpace(k.SecretAccessKey)
	}
	if strings.TrimSpace(k.SecretKey) != "" {
		return strings.TrimSpace(k.SecretKey)
	}
	return ""
}

// SecretAccessKeyField returns which response field contained the secret value.
func (k *KeyInfo) SecretAccessKeyField() string {
	if k == nil {
		return "none"
	}

	if strings.TrimSpace(k.SecretAccessKey) != "" {
		return "secretAccessKey"
	}
	if strings.TrimSpace(k.SecretKey) != "" {
		return "secretKey"
	}

	return "none"
}

// BucketPermissions defines bucket-level ACL permissions.
type BucketPermissions struct {
	Read  bool `json:"read,omitempty"`
	Write bool `json:"write,omitempty"`
	Owner bool `json:"owner,omitempty"`
}

type bucketKeyPermissionReq struct {
	BucketID    string            `json:"bucketId"`
	AccessKeyID string            `json:"accessKeyId"`
	Permissions BucketPermissions `json:"permissions"`
}

// GarageAPIError is returned for non-2xx Garage API responses.
type GarageAPIError struct {
	Method     string
	URL        string
	StatusCode int
	Message    string
	Body       string
}

func (e *GarageAPIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "Garage API request failed"
	}
	body := strings.TrimSpace(e.Body)
	if body != "" {
		return fmt.Sprintf("%s %s returned %d: %s (body: %s)", e.Method, e.URL, e.StatusCode, msg, body)
	}
	return fmt.Sprintf("%s %s returned %d: %s", e.Method, e.URL, e.StatusCode, msg)
}

// NewGarageAdminClient builds a GarageAdminClient with sane defaults.
func NewGarageAdminClient(baseURL, adminToken string, httpClient *http.Client) *GarageAdminClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}

	return &GarageAdminClient{
		baseURL:    strings.TrimSpace(baseURL),
		adminToken: strings.TrimSpace(adminToken),
		httpClient: httpClient,
	}
}

// CreateBucket creates a new bucket.
func (c *GarageAdminClient) CreateBucket(ctx context.Context, req CreateBucketReq) (*BucketInfo, error) {
	var out BucketInfo
	if err := c.doJSON(ctx, http.MethodPost, "/CreateBucket", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBucketInfo returns bucket metadata by bucket identifier.
func (c *GarageAdminClient) GetBucketInfo(ctx context.Context, bucketID string) (*BucketInfo, error) {
	var out BucketInfo
	endpoint := addQueryParam("/GetBucketInfo", "id", bucketID)
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateBucket updates bucket metadata and quotas.
func (c *GarageAdminClient) UpdateBucket(ctx context.Context, bucketID string, req UpdateBucketReq) (*BucketInfo, error) {
	var out BucketInfo
	endpoint := addQueryParam("/UpdateBucket", "id", bucketID)
	if err := c.doJSON(ctx, http.MethodPost, endpoint, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBucket deletes a bucket by identifier.
func (c *GarageAdminClient) DeleteBucket(ctx context.Context, bucketID string) error {
	endpoint := addQueryParam("/DeleteBucket", "id", bucketID)
	return c.doJSON(ctx, http.MethodPost, endpoint, nil, nil)
}

// CreateKey creates a new Garage access key.
func (c *GarageAdminClient) CreateKey(ctx context.Context, req CreateKeyReq) (*KeyInfo, error) {
	var out KeyInfo
	if err := c.doJSON(ctx, http.MethodPost, "/CreateKey", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetKeyInfo returns key metadata by key identifier.
func (c *GarageAdminClient) GetKeyInfo(ctx context.Context, keyID string) (*KeyInfo, error) {
	var out KeyInfo
	endpoint := addQueryParam("/GetKeyInfo", "id", keyID)
	if err := c.doJSON(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteKey deletes an access key by identifier.
func (c *GarageAdminClient) DeleteKey(ctx context.Context, keyID string) error {
	endpoint := addQueryParam("/DeleteKey", "id", keyID)
	return c.doJSON(ctx, http.MethodPost, endpoint, nil, nil)
}

// AllowBucketKey grants bucket permissions to a key.
func (c *GarageAdminClient) AllowBucketKey(ctx context.Context, bucketID, keyID string, perms BucketPermissions) error {
	log := logf.FromContext(ctx)
	payload := bucketKeyPermissionReq{BucketID: bucketID, AccessKeyID: keyID, Permissions: perms}
	log.V(1).Info(
		"Sending AllowBucketKey request",
		"bucketID", bucketID,
		"keyID", keyID,
		"permissions", fmt.Sprintf("read=%t write=%t owner=%t", perms.Read, perms.Write, perms.Owner),
	)
	err := c.doJSON(
		ctx,
		http.MethodPost,
		"/AllowBucketKey",
		payload,
		nil,
	)
	if err != nil {
		log.Error(err, "AllowBucketKey request failed", "bucketID", bucketID, "keyID", keyID)
		return err
	}

	log.V(1).Info("AllowBucketKey request succeeded", "bucketID", bucketID, "keyID", keyID)
	return nil
}

// DenyBucketKey revokes bucket permissions from a key.
func (c *GarageAdminClient) DenyBucketKey(ctx context.Context, bucketID, keyID string, perms BucketPermissions) error {
	log := logf.FromContext(ctx)
	payload := bucketKeyPermissionReq{BucketID: bucketID, AccessKeyID: keyID, Permissions: perms}
	log.V(1).Info(
		"Sending DenyBucketKey request",
		"bucketID", bucketID,
		"keyID", keyID,
		"permissions", fmt.Sprintf("read=%t write=%t owner=%t", perms.Read, perms.Write, perms.Owner),
	)
	err := c.doJSON(
		ctx,
		http.MethodPost,
		"/DenyBucketKey",
		payload,
		nil,
	)
	if err != nil {
		log.Error(err, "DenyBucketKey request failed", "bucketID", bucketID, "keyID", keyID)
		return err
	}

	log.V(1).Info("DenyBucketKey request succeeded", "bucketID", bucketID, "keyID", keyID)
	return nil
}

// Health checks Garage admin API liveness.
func (c *GarageAdminClient) Health(ctx context.Context) error {
	if c.baseURL == "" || c.adminToken == "" || c.httpClient == nil {
		return fmt.Errorf("garage client not properly initialized")
	}

	fullURL, err := c.makeRootURL("/health")
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.adminToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check returned %d", resp.StatusCode)
	}

	return nil
}

// CanonicalizePermissions computes which permissions need to be granted or revoked.
func CanonicalizePermissions(current, desired BucketPermissions) (BucketPermissions, BucketPermissions) {
	toAllow := BucketPermissions{}
	toDeny := BucketPermissions{}

	if desired.Read && !current.Read {
		toAllow.Read = true
	}
	if !desired.Read && current.Read {
		toDeny.Read = true
	}

	if desired.Write && !current.Write {
		toAllow.Write = true
	}
	if !desired.Write && current.Write {
		toDeny.Write = true
	}

	if desired.Owner && !current.Owner {
		toAllow.Owner = true
	}
	if !desired.Owner && current.Owner {
		toDeny.Owner = true
	}

	return toAllow, toDeny
}

func (c *GarageAdminClient) doJSON(ctx context.Context, method, requestPath string, in any, out any) error {
	if c.baseURL == "" {
		return fmt.Errorf("garage admin base URL is required")
	}
	if c.adminToken == "" {
		return fmt.Errorf("garage admin token is required")
	}
	if c.httpClient == nil {
		return fmt.Errorf("garage HTTP client is nil")
	}

	body, err := c.encodeBody(in)
	if err != nil {
		return err
	}

	fullURL, err := c.makeURL(requestPath)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return fmt.Errorf("build request %s %s: %w", method, fullURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.adminToken)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request %s %s: %w", method, fullURL, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body %s %s: %w", method, fullURL, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return c.newAPIError(method, fullURL, resp.StatusCode, respBytes)
	}

	if out == nil || len(respBytes) == 0 || resp.StatusCode == http.StatusNoContent {
		return nil
	}

	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("decode response %s %s: %w", method, fullURL, err)
	}

	return nil
}

func (c *GarageAdminClient) encodeBody(in any) (io.Reader, error) {
	if in == nil {
		return nil, nil
	}

	payload, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("encode request body: %w", err)
	}

	return bytes.NewReader(payload), nil
}

func (c *GarageAdminClient) makeURL(requestPath string) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse garage admin base URL %q: %w", c.baseURL, err)
	}

	adminPath := base.Path
	if !strings.HasSuffix(strings.TrimRight(adminPath, "/"), "/v2") {
		adminPath = path.Join(adminPath, "/v2")
	}

	requestURL, err := url.Parse(requestPath)
	if err != nil {
		return "", fmt.Errorf("parse request path %q: %w", requestPath, err)
	}
	if requestURL.IsAbs() {
		return "", fmt.Errorf("request path must be relative: %q", requestPath)
	}

	base.Path = path.Join(adminPath, requestURL.Path)
	query := base.Query()
	for key, values := range requestURL.Query() {
		for _, value := range values {
			query.Add(key, value)
		}
	}
	base.RawQuery = query.Encode()
	return base.String(), nil
}

//nolint:unparam // kept generic for endpoint query composition.
func addQueryParam(endpoint string, key, value string) string {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	return endpoint + sep + key + "=" + url.QueryEscape(value)
}

// makeRootURL constructs a URL for root-level endpoints (health, metrics)
// without the /v2 prefix.
func (c *GarageAdminClient) makeRootURL(requestPath string) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse garage admin base URL %q: %w", c.baseURL, err)
	}

	base.Path = path.Join(base.Path, requestPath)
	return base.String(), nil
}

func (c *GarageAdminClient) newAPIError(method, requestURL string, statusCode int, responseBody []byte) error {
	body := strings.TrimSpace(string(responseBody))
	if body == "" {
		return &GarageAPIError{
			Method:     method,
			URL:        requestURL,
			StatusCode: statusCode,
			Message:    http.StatusText(statusCode),
		}
	}

	var payload struct {
		Error   string `json:"error"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}

	message := ""
	if err := json.Unmarshal(responseBody, &payload); err == nil {
		message = strings.TrimSpace(strings.Join([]string{payload.Error, payload.Message, payload.Detail}, " "))
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}

	return &GarageAPIError{
		Method:     method,
		URL:        requestURL,
		StatusCode: statusCode,
		Message:    message,
		Body:       body,
	}
}
