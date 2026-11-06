package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/auth"
	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/registry"
	"github.com/fanyang89/mini-harbor/internal/retention"
	"github.com/fanyang89/mini-harbor/internal/usage"
)

type fakeRegistry struct {
	putRepository string
	putSource     string
	putTarget     string
	deletedTag    string
	deletedDigest string
	repositories  []string
	images        map[string][]registry.Image
	repositoryErr error
	imageErr      error
}

func (f *fakeRegistry) Ping(context.Context) (string, error) {
	return "registry/2.0", nil
}

func (f *fakeRegistry) Repositories(context.Context) ([]string, error) {
	if f.repositoryErr != nil {
		return nil, f.repositoryErr
	}
	if f.repositories != nil {
		return f.repositories, nil
	}
	return []string{"team/app"}, nil
}

func (f *fakeRegistry) Images(_ context.Context, repository string) ([]registry.Image, error) {
	if f.imageErr != nil {
		return nil, f.imageErr
	}
	if f.images != nil {
		return f.images[repository], nil
	}
	return []registry.Image{{Tag: "latest", Digest: "sha256:abcd", Size: 12}}, nil
}

func (f *fakeRegistry) Manifest(context.Context, string, string) (registry.Image, []byte, string, error) {
	return registry.Image{}, nil, "", nil
}

func (f *fakeRegistry) PutTag(_ context.Context, repository, source, target string) (registry.Image, error) {
	f.putRepository, f.putSource, f.putTarget = repository, source, target
	return registry.Image{Tag: target, Digest: "sha256:abcd"}, nil
}

func (f *fakeRegistry) DeleteTag(_ context.Context, _ string, tag string) error {
	f.deletedTag = tag
	return nil
}

func (f *fakeRegistry) DeleteManifest(_ context.Context, _ string, digest string) error {
	f.deletedDigest = digest
	return nil
}

func TestRepositoryRoutesSupportNestedNames(t *testing.T) {
	client := &fakeRegistry{}
	handler := New(client, nil, nil, "localhost:5000", t.TempDir(), auth.Disabled())

	request := httptest.NewRequest(http.MethodPost, "/api/repositories/team/app/tags", jsonBody(`{"source":"latest","target":"stable"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("unexpected status: %d, body: %s", response.Code, response.Body.String())
	}
	if client.putRepository != "team/app" || client.putSource != "latest" || client.putTarget != "stable" {
		t.Fatalf("unexpected put call: %#v", client)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/repositories/team/app/tags/stable", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || client.deletedTag != "stable" {
		t.Fatalf("tag delete failed: status=%d tag=%q", response.Code, client.deletedTag)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/repositories/team/app/manifests/sha256:abcd", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || client.deletedDigest != "sha256:abcd" {
		t.Fatalf("manifest delete failed: status=%d digest=%q", response.Code, client.deletedDigest)
	}
}

func TestManifestDeleteRejectsTag(t *testing.T) {
	handler := New(&fakeRegistry{}, nil, nil, "localhost:5000", t.TempDir(), auth.Disabled())
	request := httptest.NewRequest(http.MethodDelete, "/api/repositories/team/app/manifests/latest", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}

func TestStatusResponse(t *testing.T) {
	handler := New(&fakeRegistry{}, nil, nil, "registry.example.test", t.TempDir(), auth.Disabled())
	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["online"] != true || payload["registry"] != "registry.example.test" {
		t.Fatalf("unexpected response: %#v", payload)
	}
}

func TestImagesIncludeLastUsed(t *testing.T) {
	usageStore, err := usage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	usedAt := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	if err := usageStore.Record("team/app", "sha256:abcd", usedAt); err != nil {
		t.Fatal(err)
	}
	handler := New(&fakeRegistry{}, nil, usageStore, "localhost:5000", t.TempDir(), auth.Disabled())
	request := httptest.NewRequest(http.MethodGet, "/api/repositories/team/app/tags", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	var payload struct {
		Images []registry.Image `json:"images"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(payload.Images) != 1 || payload.Images[0].LastUsed != usedAt.Format(time.RFC3339Nano) {
		t.Fatalf("unexpected images response: status=%d payload=%#v", response.Code, payload)
	}
}

func TestImageGCPreviewAndExecute(t *testing.T) {
	usageStore, err := usage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := usageStore.Record("team/app", "sha256:abcd", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := &fakeRegistry{}
	handler := New(client, nil, usageStore, "localhost:5000", t.TempDir(), auth.Disabled())

	request := httptest.NewRequest(http.MethodPost, "/api/image-gc/preview", jsonBody(`{"unusedForDays":1}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var preview struct {
		PlanID        string `json:"planId"`
		ManifestCount int    `json:"manifestCount"`
		TagCount      int    `json:"tagCount"`
		LogicalSize   int64  `json:"logicalSize"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || preview.PlanID == "" || preview.ManifestCount != 1 || preview.TagCount != 1 || preview.LogicalSize != 12 {
		t.Fatalf("preview status=%d payload=%#v body=%s", response.Code, preview, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/image-gc/execute", jsonBody(`{"planId":"`+preview.PlanID+`","confirmation":"no"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || client.deletedDigest != "" {
		t.Fatalf("confirmation status=%d deleted=%q", response.Code, client.deletedDigest)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/image-gc/execute", jsonBody(`{"planId":"`+preview.PlanID+`","confirmation":"DELETE"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var execution struct {
		DeletedCount int `json:"deletedCount"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || execution.DeletedCount != 1 || client.deletedDigest != "sha256:abcd" {
		t.Fatalf("execute status=%d payload=%#v deleted=%q body=%s", response.Code, execution, client.deletedDigest, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/image-gc/execute", jsonBody(`{"planId":"`+preview.PlanID+`","confirmation":"DELETE"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || client.deletedDigest != "sha256:abcd" {
		t.Fatalf("replayed plan status=%d deleted=%q body=%s", response.Code, client.deletedDigest, response.Body.String())
	}
}

func TestImageGCRequestsUseStrictJSON(t *testing.T) {
	handler := New(&fakeRegistry{}, nil, nil, "localhost:5000", t.TempDir(), auth.Disabled())
	tests := []struct {
		path string
		body string
	}{
		{path: "/api/image-gc/preview", body: `{"unusedForDays":0}`},
		{path: "/api/image-gc/preview", body: `{"unusedForDays":3651}`},
		{path: "/api/image-gc/preview", body: `{"unusedForDays":1.5}`},
		{path: "/api/image-gc/preview", body: `{"unusedForDays":1,"extra":true}`},
		{path: "/api/image-gc/preview", body: `{"unusedForDays":1} {}`},
		{path: "/api/image-gc/execute", body: `{"planId":"x","confirmation":"delete"}`},
		{path: "/api/image-gc/execute", body: `{"planId":"","confirmation":"DELETE"}`},
		{path: "/api/image-gc/execute", body: `{"planId":"x","confirmation":"DELETE","extra":true}`},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodPost, test.path, jsonBody(test.body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s body %s: status=%d response=%s", test.path, test.body, response.Code, response.Body.String())
		}
	}
}

func TestImageGCPreviewMapsDependencyErrors(t *testing.T) {
	usageStore, err := usage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		client *fakeRegistry
		usage  ImageUsage
		status int
	}{
		{name: "registry", client: &fakeRegistry{repositoryErr: errors.New("offline")}, usage: usageStore, status: http.StatusBadGateway},
		{name: "storage", client: &fakeRegistry{}, usage: nil, status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := New(test.client, nil, test.usage, "localhost:5000", t.TempDir(), auth.Disabled())
			request := httptest.NewRequest(http.MethodPost, "/api/image-gc/preview", jsonBody(`{"unusedForDays":1}`))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestImageGCErrorStatuses(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{err: retention.ErrInvalidUnusedForDays, status: http.StatusBadRequest},
		{err: retention.ErrCandidateLimit, status: http.StatusUnprocessableEntity},
		{err: retention.ErrPlanNotFound, status: http.StatusNotFound},
		{err: retention.ErrPlanExpired, status: http.StatusGone},
		{err: retention.ErrPlanUsed, status: http.StatusConflict},
		{err: retention.ErrRegistry, status: http.StatusBadGateway},
		{err: retention.ErrUsage, status: http.StatusInternalServerError},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		new(server).retentionError(response, test.err)
		if response.Code != test.status {
			t.Errorf("error %v: status=%d body=%s", test.err, response.Code, response.Body.String())
		}
	}
}

func TestOrganizationCRUD(t *testing.T) {
	store, err := organization.Open(context.Background(), t.TempDir()+"/mini-harbor.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler := New(&fakeRegistry{}, store, nil, "localhost:5000", t.TempDir(), auth.Disabled())

	request := httptest.NewRequest(http.MethodPost, "/api/organizations", jsonBody(`{"name":"platform","displayName":"Platform","description":"Shared images"}`))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Location") != "/api/organizations/platform" {
		t.Fatalf("create status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPut, "/api/organizations/platform", jsonBody(`{"displayName":"Core Platform","description":"Production images"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/organizations", jsonBody(`{"name":"team","displayName":"Team"}`))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("team create status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/organizations", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload struct {
		Organizations []struct {
			Name            string `json:"name"`
			DisplayName     string `json:"displayName"`
			RepositoryCount int    `json:"repositoryCount"`
		} `json:"organizations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(payload.Organizations) != 2 || payload.Organizations[0].DisplayName != "Core Platform" || payload.Organizations[1].RepositoryCount != 1 {
		t.Fatalf("unexpected organizations: status=%d payload=%#v", response.Code, payload)
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/organizations/team", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("non-empty delete status=%d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/organizations/platform", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
}

func jsonBody(value string) *strings.Reader {
	return strings.NewReader(value)
}
