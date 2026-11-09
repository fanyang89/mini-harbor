package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/auth"
	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/registry"
	"github.com/fanyang89/mini-harbor/internal/usage"
)

type graphTestResponse struct {
	Data   map[string]json.RawMessage
	Errors []struct {
		Message    string
		Extensions struct{ Status int }
	}
}

func graphTestRequest(ctx context.Context, handler http.Handler, method, query string, variables map[string]any) (*httptest.ResponseRecorder, graphTestResponse) {
	data, _ := json.Marshal(map[string]any{"query": query, "variables": variables})
	request := httptest.NewRequest(method, "/graphql", bytes.NewReader(data)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	if method == http.MethodGet {
		vars, _ := json.Marshal(variables)
		request.URL.RawQuery = url.Values{"query": {query}, "variables": {string(vars)}}.Encode()
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var result graphTestResponse
	_ = json.Unmarshal(response.Body.Bytes(), &result)
	return response, result
}
func requireGraphOK(t *testing.T, response *httptest.ResponseRecorder, result graphTestResponse) {
	t.Helper()
	if response.Code != 200 || len(result.Errors) > 0 || result.Data == nil {
		t.Fatalf("GraphQL failed: %d %s", response.Code, response.Body.String())
	}
}

func TestGraphQLGlobalPaginationAndCursorScope(t *testing.T) {
	client := &fakeRegistry{repositories: []string{"team/z", "team/a", "other/app"}, images: map[string][]registry.Image{
		"team/z":    {{Tag: "z", Digest: "sha256:ff", Size: 1 << 34, LastUsed: "2026-01-01T00:00:00Z"}},
		"team/a":    {{Tag: "a", Digest: "sha256:aa", Size: 5}, {Tag: "b", Digest: "sha256:bb", Size: 5}},
		"other/app": {{Tag: "other", Digest: "sha256:cc", Size: 1}},
	}}
	// A cursor must work on another replica, without sticky sessions or a cache.
	handlers := []http.Handler{New(client, nil, nil, "registry", t.TempDir(), nil), New(client, nil, nil, "registry", t.TempDir(), nil)}
	query := `query Page($after: String!, $filter: ImageFilter!) { images(first: 1, after: $after, filter: $filter, sort: SIZE, direction: DESC) { nodes { repository tag size } totalCount pageInfo { endCursor hasNextPage } } }`
	cursor := ""
	var tags []string
	for i := range 3 {
		response, result := graphTestRequest(t.Context(), handlers[i%2], "GET", query, map[string]any{"after": cursor, "filter": map[string]any{"organization": "team"}})
		requireGraphOK(t, response, result)
		var page struct {
			Nodes []struct {
				Tag  string
				Size int64
			}
			TotalCount int
			PageInfo   graphPageInfo
		}
		if err := json.Unmarshal(result.Data["images"], &page); err != nil {
			t.Fatal(err)
		}
		if page.TotalCount != 3 || len(page.Nodes) != 1 || page.PageInfo.HasNextPage != (i < 2) {
			t.Fatalf("bad page: %s", response.Body.String())
		}
		if i == 0 && page.Nodes[0].Size != 1<<34 {
			t.Fatal("GraphQL truncated byte count")
		}
		tags = append(tags, page.Nodes[0].Tag)
		cursor = page.PageInfo.EndCursor
	}
	if !slices.Equal(tags, []string{"z", "a", "b"}) {
		t.Fatalf("unstable order: %v", tags)
	}
	_, result := graphTestRequest(t.Context(), handlers[0], "GET", query, map[string]any{"after": cursor, "filter": map[string]any{"search": "other"}})
	if len(result.Errors) == 0 || result.Errors[0].Extensions.Status != 400 {
		t.Fatalf("accepted mismatched cursor: %+v", result)
	}
	response, result := graphTestRequest(t.Context(), handlers[0], "GET", `{ images(first: 1, filter: {search: "other"}) { nodes { tag } totalCount } }`, nil)
	requireGraphOK(t, response, result)
	if !strings.Contains(string(result.Data["images"]), `"other"`) {
		t.Fatalf("search missed unloaded repository: %s", response.Body.String())
	}
	for _, bad := range []string{`{ images(first: 0) { totalCount } }`, `{ images(first: 101) { totalCount } }`, `{ images(after: "bad") { totalCount } }`, `{ images(sort: INVALID) { totalCount } }`} {
		_, result := graphTestRequest(t.Context(), handlers[0], "GET", bad, nil)
		if len(result.Errors) == 0 {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestGraphQLAliasesShareReadsAndMissingTimesSortLast(t *testing.T) {
	var calls atomic.Int32
	client := &batchRegistry{fakeRegistry: fakeRegistry{repositories: []string{"team/z", "team/a", "stale", "team/a"}}, imagesFunc: func(_ context.Context, name string) ([]registry.Image, error) {
		calls.Add(1)
		if name == "stale" {
			return nil, &registry.Error{StatusCode: 404}
		}
		image := registry.Image{Tag: name, Size: 1}
		if name == "team/z" {
			image.Created = "2026-01-01T00:00:00Z"
		}
		return []registry.Image{image}, nil
	}}
	handler := New(client, nil, nil, "registry", t.TempDir(), nil)
	response, result := graphTestRequest(t.Context(), handler, "GET", `{ a: images(sort: CREATED, direction: ASC) { nodes { tag } } b: images(sort: CREATED, direction: DESC) { nodes { tag } } repositories { nodes } }`, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 3 {
		t.Fatalf("aliases repeated scans: %d", calls.Load())
	}
	for _, alias := range []string{"a", "b"} {
		var page struct{ Nodes []struct{ Tag string } }
		_ = json.Unmarshal(result.Data[alias], &page)
		if len(page.Nodes) != 2 || page.Nodes[0].Tag != "team/z" {
			t.Fatalf("missing timestamps not last: %s", response.Body.String())
		}
	}
}

func TestGraphQLCancellationBoundsWorkers(t *testing.T) {
	started := make(chan struct{}, 20)
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(ctx context.Context, _ string) ([]registry.Image, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	for i := range 20 {
		client.repositories = append(client.repositories, fmt.Sprintf("repo%d", i))
	}
	handler := New(client, nil, nil, "registry", t.TempDir(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { graphTestRequest(ctx, handler, "GET", `{ images { totalCount } }`, nil); close(done) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not start")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop scan")
	}
	if calls.Load() != 4 {
		t.Fatalf("queued work ran after cancellation: %d", calls.Load())
	}
}

func TestGraphQLPublicReadsCannotMutate(t *testing.T) {
	var issuer string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]string{"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys"})
	}))
	defer provider.Close()
	issuer = provider.URL
	authentication, err := auth.New(t.Context(), auth.Config{Issuer: issuer, ClientID: "test", RedirectURL: "http://example.com/auth/callback", SessionSecret: []byte(strings.Repeat("s", 32))})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeRegistry{}
	handler := New(client, nil, nil, "registry", t.TempDir(), authentication)
	response, result := graphTestRequest(t.Context(), handler, "GET", `{ status { online } }`, nil)
	requireGraphOK(t, response, result)
	mutation := `mutation { deleteTag(repository: "team/app", tag: "latest") }`
	_, result = graphTestRequest(t.Context(), handler, "GET", mutation, nil)
	if len(result.Errors) == 0 || client.deletedTag != "" {
		t.Fatal("GET mutation executed")
	}
	response, _ = graphTestRequest(t.Context(), handler, "POST", mutation, nil)
	if response.Code != 401 || client.deletedTag != "" {
		t.Fatalf("unauthenticated mutation executed: %s", response.Body.String())
	}
}

type graphTestOrganizations struct {
	items map[string]organization.Organization
}

func (s *graphTestOrganizations) List(context.Context) ([]organization.Organization, error) {
	items := []organization.Organization{}
	for _, item := range s.items {
		items = append(items, item)
	}
	return items, nil
}
func (s *graphTestOrganizations) Get(_ context.Context, name string) (organization.Organization, error) {
	item, ok := s.items[name]
	if !ok {
		return item, organization.ErrNotFound
	}
	return item, nil
}
func (s *graphTestOrganizations) Create(_ context.Context, name, display, description string) (organization.Organization, error) {
	if _, ok := s.items[name]; ok {
		return organization.Organization{}, organization.ErrExists
	}
	item := organization.Organization{Name: name, DisplayName: display, Description: description, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.items[name] = item
	return item, nil
}
func (s *graphTestOrganizations) Update(ctx context.Context, name, display, description string) (organization.Organization, error) {
	item, err := s.Get(ctx, name)
	if err != nil {
		return item, err
	}
	item.DisplayName, item.Description = display, description
	s.items[name] = item
	return item, nil
}
func (s *graphTestOrganizations) Delete(ctx context.Context, name string) error {
	if _, err := s.Get(ctx, name); err != nil {
		return err
	}
	delete(s.items, name)
	return nil
}

func TestGraphQLMutationsAndOrganizationLazyLoading(t *testing.T) {
	var calls atomic.Int32
	client := &batchRegistry{imagesFunc: func(context.Context, string) ([]registry.Image, error) { calls.Add(1); return nil, nil }}
	store := &graphTestOrganizations{items: map[string]organization.Organization{}}
	handler := New(client, store, nil, "registry", t.TempDir(), nil)
	for _, query := range []string{
		`mutation { createOrganization(input: {name:"empty", displayName:"Empty"}) { name createdAt } }`,
		`mutation { updateOrganization(input: {name:"empty", displayName:"Updated"}) { displayName } }`,
		`mutation { putTag(repository:"team/nested/app", source:"latest", target:"stable") { tag size } }`,
		`mutation { deleteTag(repository:"team/nested/app", tag:"stable") }`,
		`mutation { deleteManifest(repository:"team/nested/app", digest:"sha256:abcd") }`,
		`mutation { deleteOrganization(name:"empty") }`,
	} {
		response, result := graphTestRequest(t.Context(), handler, "POST", query, nil)
		requireGraphOK(t, response, result)
	}
	response, result := graphTestRequest(t.Context(), handler, "GET", `{ organizations { name repositoryCount } }`, nil)
	requireGraphOK(t, response, result)
	if calls.Load() != 0 {
		t.Fatal("organizations loaded image metadata")
	}
	if client.putRepository != "team/nested/app" || client.deletedTag != "stable" || client.deletedDigest != "sha256:abcd" {
		t.Fatalf("mutations missed registry: %+v", client)
	}
	for _, query := range []string{
		`mutation { deleteOrganization(name:"team") }`,
		`mutation { deleteManifest(repository:"team/app", digest:"latest") }`,
		`mutation { putTag(repository:"../bad", source:"latest", target:"stable") { tag } }`,
		`mutation { createOrganization(input: {name:"BAD", displayName:"Bad"}) { name } }`,
	} {
		_, result := graphTestRequest(t.Context(), handler, "POST", query, nil)
		if len(result.Errors) == 0 {
			t.Fatalf("accepted unsafe mutation: %s", query)
		}
	}
}

func TestGraphQLCleanupConfirmationAndReplay(t *testing.T) {
	store, err := usage.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Record("team/app", "sha256:abcd", time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := &fakeRegistry{}
	handler := New(client, nil, store, "registry", t.TempDir(), nil)
	response, result := graphTestRequest(t.Context(), handler, "POST", `mutation { previewImageGC(unusedForDays:1) { planId candidates { tags logicalSize } } }`, nil)
	requireGraphOK(t, response, result)
	var preview struct{ PlanID string }
	_ = json.Unmarshal(result.Data["previewImageGC"], &preview)
	query := `mutation Execute($id:String!, $confirmation:String!) { executeImageGC(planId:$id, confirmation:$confirmation) { deletedCount results { status reason } } }`
	_, result = graphTestRequest(t.Context(), handler, "POST", query, map[string]any{"id": preview.PlanID, "confirmation": "no"})
	if len(result.Errors) == 0 || client.deletedDigest != "" {
		t.Fatal("cleanup ignored confirmation")
	}
	for range 2 {
		response, result = graphTestRequest(t.Context(), handler, "POST", query, map[string]any{"id": preview.PlanID, "confirmation": "DELETE"})
		requireGraphOK(t, response, result)
		if client.deletedDigest != "sha256:abcd" {
			t.Fatal("cleanup did not delete expected manifest")
		}
	}
}

func TestGraphQLTransportAndDependencyErrors(t *testing.T) {
	handler := New(&fakeRegistry{imageErr: fmt.Errorf("registry unavailable")}, nil, nil, "registry", t.TempDir(), nil)
	response, result := graphTestRequest(t.Context(), handler, "GET", `{ images { nodes { tag } totalCount } }`, nil)
	if len(result.Errors) == 0 || result.Errors[0].Extensions.Status != 502 || string(result.Data["images"]) != "" {
		t.Fatalf("published incomplete catalog: %s", response.Body.String())
	}
	for _, test := range []struct {
		method, body, contentType string
		status                    int
	}{
		{"POST", `{}`, "text/plain", 415},
		{"POST", `[]`, "application/json", 400},
		{"POST", `{"query":"{ status { online } }"} {}`, "application/json", 400},
		{"POST", `{"query":"` + strings.Repeat(" ", 65<<10) + `"}`, "application/json", 400},
		{"DELETE", "", "", 405},
	} {
		request := httptest.NewRequest(test.method, "/graphql", strings.NewReader(test.body))
		request.Header.Set("Content-Type", test.contentType)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("transport status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
		}
	}
}
