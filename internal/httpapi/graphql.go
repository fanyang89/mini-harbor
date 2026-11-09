package httpapi

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/fanyang89/mini-harbor/internal/auth"
	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/registry"
	graphql "github.com/graph-gophers/graphql-go"
)

//go:embed schema.graphql
var querySchema string

//go:embed mutations.graphql
var mutationSchema string

type graphResolver struct{ server *server }
type graphRequestKey struct{}

// Request-local memoization prevents aliases and sibling resolvers from repeating
// catalog scans. Nothing is retained across requests or shared between users.
type graphRequest struct {
	fresh            bool
	background       bool
	catalogDuration  time.Duration
	catalogSource    string
	repositoriesOnce sync.Once
	repositories     []string
	repositoriesErr  error
	imagesOnce       sync.Once
	images           []*graphImage
	imagesErr        error
}

func (s *server) registerGraphQL(mux *http.ServeMux, authentication *auth.Authenticator) {
	options := []graphql.SchemaOpt{graphql.UseFieldResolvers(), graphql.MaxDepth(12), graphql.MaxParallelism(4), graphql.MaxQueryLength(16 << 10)}
	resolver := &graphResolver{server: s}
	// GET uses a schema without mutations, preserving public read-only access.
	// POST uses the same session and same-origin checks as existing REST writes.
	readSchema := graphql.MustParseSchema(querySchema, resolver, options...)
	writeSchema := graphql.MustParseSchema(querySchema+mutationSchema, resolver, options...)
	handler := func(schema *graphql.Schema) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			var input struct {
				Query         string         `json:"query"`
				OperationName string         `json:"operationName"`
				Variables     map[string]any `json:"variables"`
			}
			if r.Method == http.MethodGet {
				if len(r.URL.RawQuery) > 32<<10 {
					writeError(w, http.StatusRequestURITooLong, "GraphQL request too large")
					return
				}
				input.Query = r.URL.Query().Get("query")
				input.OperationName = r.URL.Query().Get("operationName")
				if variables := r.URL.Query().Get("variables"); variables != "" {
					if err := json.Unmarshal([]byte(variables), &input.Variables); err != nil {
						writeError(w, http.StatusBadRequest, "Invalid GraphQL variables")
						return
					}
				}
			} else {
				mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if mediaType != "application/json" {
					writeError(w, http.StatusUnsupportedMediaType, "Use application/json")
					return
				}
				r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
				if !decodeBody(w, r, &input) {
					return
				}
			}
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			state := &graphRequest{fresh: r.Header.Get("Cache-Control") == "no-cache"}
			ctx = context.WithValue(ctx, graphRequestKey{}, state)
			result := schema.Exec(ctx, input.Query, input.OperationName, input.Variables)
			if state.catalogDuration > 0 {
				w.Header().Set("Server-Timing", fmt.Sprintf("catalog;dur=%.1f;desc=%q", float64(state.catalogDuration)/float64(time.Millisecond), state.catalogSource))
			}
			writeJSON(w, http.StatusOK, result)
		}
	}
	mux.Handle("GET /graphql", handler(readSchema))
	mux.Handle("POST /graphql", authentication.RequireAPI(handler(writeSchema)))
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "GET, HEAD, POST")
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	})
}

type graphError struct {
	message string
	status  int
}

func (e graphError) Error() string              { return e.message }
func (e graphError) Extensions() map[string]any { return map[string]any{"status": e.status} }
func invalidGraph(message string) error         { return graphError{message, http.StatusBadRequest} }

func graphRegistryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errImageUsage) {
		return graphError{err.Error(), http.StatusInternalServerError}
	}
	if e, ok := errors.AsType[*registry.Error](err); ok {
		switch e.StatusCode {
		case http.StatusNotFound, http.StatusBadRequest, http.StatusConflict:
			return graphError{e.Error(), e.StatusCode}
		case http.StatusMethodNotAllowed:
			return graphError{e.Error(), http.StatusConflict}
		}
	}
	return graphError{err.Error(), http.StatusBadGateway}
}

func graphOrganizationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, organization.ErrNotFound):
		return graphError{"Organization not found", 404}
	case errors.Is(err, organization.ErrExists):
		return graphError{"Organization already exists", 409}
	case errors.Is(err, organization.ErrOutcomeUnknown):
		return graphError{"Write outcome unknown; reload the organization before retrying", 500}
	default:
		return graphError{"Organization storage failed", 500}
	}
}

// Long preserves byte counts above GraphQL's signed 32-bit Int range.
type graphLong int64

func (*graphLong) UnmarshalGraphQL(any) error { return errors.New("Long is an output-only scalar") }

func (graphLong) ImplementsGraphQLType(name string) bool { return name == "Long" }
func (v graphLong) MarshalJSON() ([]byte, error)         { return json.Marshal(int64(v)) }

type graphImage struct {
	Repository   string
	Tag          string
	Digest       string
	MediaType    string
	Size         graphLong
	Layers       int32
	Platforms    []registry.Platform
	Created      string
	LastUsed     string
	Architecture string
	OS           string
}

func imageGraph(repository string, image registry.Image) *graphImage {
	return &graphImage{repository, image.Tag, image.Digest, image.MediaType, graphLong(image.Size), int32(image.Layers), image.Platforms, image.Created, image.LastUsed, image.Architecture, image.OS}
}

type graphStatus struct {
	Online     bool
	Registry   string
	APIVersion string
}

func (r *graphResolver) Status(ctx context.Context) *graphStatus {
	version, err := r.server.registry.Ping(ctx)
	return &graphStatus{err == nil, r.server.publicURL, version}
}

type graphOrganization struct {
	Name            string
	DisplayName     string
	Description     string
	CreatedAt       string
	UpdatedAt       string
	RepositoryCount int32
}

func organizationGraph(item organization.Organization, repositories []string) *graphOrganization {
	return &graphOrganization{item.Name, item.DisplayName, item.Description, item.CreatedAt.Format(time.RFC3339Nano), item.UpdatedAt.Format(time.RFC3339Nano), int32(repositoryCount(item.Name, repositories))}
}
func (r *graphResolver) Organizations(ctx context.Context) ([]*graphOrganization, error) {
	items, err := r.server.organizations.List(ctx)
	if err != nil {
		return nil, graphOrganizationError(err)
	}
	repositories, err := r.catalogRepositories(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*graphOrganization, 0, len(items))
	for _, item := range items {
		result = append(result, organizationGraph(item, repositories))
	}
	return result, nil
}
func (r *graphResolver) Image(ctx context.Context, args struct{ Repository, Reference string }) (*graphImage, error) {
	if !validRepository(args.Repository) || (!tagPattern.MatchString(args.Reference) && !digestPattern.MatchString(args.Reference)) {
		return nil, invalidGraph("Invalid repository or reference")
	}
	select {
	case r.server.imageSlots <- struct{}{}:
		defer func() { <-r.server.imageSlots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	image, _, _, err := r.server.registry.Manifest(ctx, args.Repository, args.Reference)
	if err != nil {
		return nil, graphRegistryError(err)
	}
	image.Tag = args.Reference
	return imageGraph(args.Repository, image), nil
}
