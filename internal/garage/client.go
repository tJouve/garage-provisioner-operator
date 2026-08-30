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

// KeyBucketPermissions captures key permissions for one bucket.
type KeyBucketPermissions struct {
	Permissions BucketPermissions `json:"permissions,omitempty"`
	Read        bool              `json:"read,omitempty"`
	Write       bool              `json:"write,omitempty"`
	Owner       bool              `json:"owner,omitempty"`
}

// KeyInfo defines key information returned by Garage.
type KeyInfo struct {
	ID              string                          `json:"id,omitempty"`
	Name            string                          `json:"name,omitempty"`
	AccessKeyID     string                          `json:"accessKeyId,omitempty"`
	SecretKey       string                          `json:"secretKey,omitempty"`
	SecretAccessKey string                          `json:"secretAccessKey,omitempty"`
	Metadata        map[string]string               `json:"metadata,omitempty"`
	BucketPerms     map[string]BucketPermissions    `json:"bucketPermissions,omitempty"`
	Permissions     map[string]BucketPermissions    `json:"permissions,omitempty"`
	Buckets         map[string]KeyBucketPermissions `json:"buckets,omitempty"`
	CreatedAt       *time.Time                      `json:"createdAt,omitempty"`
}

// BucketPermissions defines bucket-level ACL permissions.
type BucketPermissions struct {
	Read  bool `json:"read,omitempty"`
	Write bool `json:"write,omitempty"`
	Owner bool `json:"owner,omitempty"`
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
	if err := c.doJSON(ctx, http.MethodPost, "/bucket", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetBucketInfo returns bucket metadata by bucket identifier.
func (c *GarageAdminClient) GetBucketInfo(ctx context.Context, bucketID string) (*BucketInfo, error) {
	var out BucketInfo
	if err := c.doJSON(ctx, http.MethodGet, "/bucket/"+url.PathEscape(bucketID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateBucket updates bucket metadata and quotas.
func (c *GarageAdminClient) UpdateBucket(ctx context.Context, bucketID string, req UpdateBucketReq) (*BucketInfo, error) {
	var out BucketInfo
	if err := c.doJSON(ctx, http.MethodPut, "/bucket/"+url.PathEscape(bucketID), req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteBucket deletes a bucket by identifier.
func (c *GarageAdminClient) DeleteBucket(ctx context.Context, bucketID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/bucket/"+url.PathEscape(bucketID), nil, nil)
}

// CreateKey creates a new Garage access key.
func (c *GarageAdminClient) CreateKey(ctx context.Context, req CreateKeyReq) (*KeyInfo, error) {
	var out KeyInfo
	if err := c.doJSON(ctx, http.MethodPost, "/key", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetKeyInfo returns key metadata by key identifier.
func (c *GarageAdminClient) GetKeyInfo(ctx context.Context, keyID string) (*KeyInfo, error) {
	var out KeyInfo
	if err := c.doJSON(ctx, http.MethodGet, "/key/"+url.PathEscape(keyID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteKey deletes an access key by identifier.
func (c *GarageAdminClient) DeleteKey(ctx context.Context, keyID string) error {
	return c.doJSON(ctx, http.MethodDelete, "/key/"+url.PathEscape(keyID), nil, nil)
}

// AllowBucketKey grants bucket permissions to a key.
func (c *GarageAdminClient) AllowBucketKey(ctx context.Context, bucketID, keyID string, perms BucketPermissions) error {
	return c.doJSON(
		ctx,
		http.MethodPost,
		"/bucket/"+url.PathEscape(bucketID)+"/key/"+url.PathEscape(keyID)+"/allow",
		perms,
		nil,
	)
}

// DenyBucketKey revokes bucket permissions from a key.
func (c *GarageAdminClient) DenyBucketKey(ctx context.Context, bucketID, keyID string, perms BucketPermissions) error {
	return c.doJSON(
		ctx,
		http.MethodPost,
		"/bucket/"+url.PathEscape(bucketID)+"/key/"+url.PathEscape(keyID)+"/deny",
		perms,
		nil,
	)
}

// Health checks Garage admin API liveness.
func (c *GarageAdminClient) Health(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodGet, "/health", nil, nil)
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

	base.Path = path.Join(adminPath, requestPath)
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
