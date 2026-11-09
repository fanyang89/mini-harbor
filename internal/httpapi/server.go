package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fanyang89/mini-harbor/internal/auth"
	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/registry"
	"github.com/fanyang89/mini-harbor/internal/retention"
)

var (
	repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
	tagPattern        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
	digestPattern     = regexp.MustCompile(`^[A-Za-z0-9_+.-]+:[A-Fa-f0-9]+$`)
)

type Registry interface {
	Ping(ctx context.Context) (string, error)
	Repositories(ctx context.Context) ([]string, error)
	Images(ctx context.Context, repository string) ([]registry.Image, error)
	Manifest(ctx context.Context, repository, reference string) (registry.Image, []byte, string, error)
	PutTag(ctx context.Context, repository, source, target string) (registry.Image, error)
	DeleteTag(ctx context.Context, repository, tag string) error
	DeleteManifest(ctx context.Context, repository, digest string) error
}

type Organizations interface {
	List(ctx context.Context) ([]organization.Organization, error)
	Get(ctx context.Context, name string) (organization.Organization, error)
	Create(ctx context.Context, name, displayName, description string) (organization.Organization, error)
	Update(ctx context.Context, name, displayName, description string) (organization.Organization, error)
	Delete(ctx context.Context, name string) error
}

type ImageUsage interface {
	LastUsed(repository, digest string) (time.Time, bool, error)
}

type server struct {
	registry          Registry
	organizations     Organizations
	publicURL         string
	webRoot           string
	imageUsage        ImageUsage
	retention         *retention.Service
	imageSlots        chan struct{}
	catalogMu         sync.Mutex
	catalogFlight     chan struct{}
	catalogBackground atomic.Bool
}

type Handler struct {
	http.Handler
	server *server
}

func New(client Registry, organizations Organizations, imageUsage ImageUsage, publicURL, webRoot string, authentication *auth.Authenticator, plans ...retention.PlanStore) *Handler {
	server := &server{
		registry:      client,
		organizations: organizations,
		imageUsage:    imageUsage,
		retention:     retention.New(client, imageUsage, plans...),
		publicURL:     publicURL,
		webRoot:       webRoot,
		imageSlots:    make(chan struct{}, 4),
	}
	if authentication == nil {
		authentication = auth.Disabled()
	}
	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", server.status)
	api.HandleFunc("GET /api/organizations", server.listOrganizations)
	api.HandleFunc("POST /api/organizations", server.createOrganization)
	api.HandleFunc("GET /api/organizations/{name}", server.getOrganization)
	api.HandleFunc("PUT /api/organizations/{name}", server.updateOrganization)
	api.HandleFunc("DELETE /api/organizations/{name}", server.deleteOrganization)
	api.HandleFunc("GET /api/repositories", server.repositories)
	api.HandleFunc("GET /api/images", server.imageBatch)
	api.HandleFunc("POST /api/image-gc/preview", server.previewImageGC)
	api.HandleFunc("POST /api/image-gc/execute", server.executeImageGC)
	api.HandleFunc("/api/repositories/", server.repository)
	api.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "API endpoint not found")
	})

	mux := http.NewServeMux()
	server.registerGraphQL(mux, authentication)
	authentication.Register(mux)
	mux.HandleFunc("GET /api/auth/me", authentication.Status)
	mux.Handle("/api/", authentication.RequireAPI(api))
	mux.Handle("/", server.web())
	return &Handler{Handler: securityHeaders(mux), server: server}
}

type organizationResponse struct {
	organization.Organization
	RepositoryCount int `json:"repositoryCount"`
}

func (s *server) listOrganizations(w http.ResponseWriter, r *http.Request) {
	organizations, err := s.organizations.List(r.Context())
	if err != nil {
		s.organizationError(w, err)
		return
	}
	repositories, err := s.registry.Repositories(r.Context())
	if err != nil {
		s.registryError(w, err)
		return
	}
	response := make([]organizationResponse, len(organizations))
	for i, item := range organizations {
		response[i] = organizationResponse{Organization: item, RepositoryCount: repositoryCount(item.Name, repositories)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": response})
}

func (s *server) getOrganization(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !organization.ValidName(name) {
		writeError(w, http.StatusBadRequest, "Invalid organization name")
		return
	}
	item, err := s.organizations.Get(r.Context(), name)
	if err != nil {
		s.organizationError(w, err)
		return
	}
	repositories, err := s.registry.Repositories(r.Context())
	if err != nil {
		s.registryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, organizationResponse{Organization: item, RepositoryCount: repositoryCount(name, repositories)})
}

func (s *server) createOrganization(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Description = strings.TrimSpace(input.Description)
	if input.DisplayName == "" {
		input.DisplayName = input.Name
	}
	if !organization.ValidName(input.Name) {
		writeError(w, http.StatusBadRequest, "Invalid organization name")
		return
	}
	if !validOrganizationDetails(input.DisplayName, input.Description) {
		writeError(w, http.StatusBadRequest, "Invalid organization details")
		return
	}
	repositories, err := s.registry.Repositories(r.Context())
	if err != nil {
		s.registryError(w, err)
		return
	}
	item, err := s.organizations.Create(r.Context(), input.Name, input.DisplayName, input.Description)
	if err != nil {
		s.organizationError(w, err)
		return
	}
	w.Header().Set("Location", "/api/organizations/"+url.PathEscape(item.Name))
	writeJSON(w, http.StatusCreated, organizationResponse{Organization: item, RepositoryCount: repositoryCount(item.Name, repositories)})
}

func (s *server) updateOrganization(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !organization.ValidName(name) {
		writeError(w, http.StatusBadRequest, "Invalid organization name")
		return
	}
	var input struct {
		DisplayName string `json:"displayName"`
		Description string `json:"description"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Description = strings.TrimSpace(input.Description)
	if !validOrganizationDetails(input.DisplayName, input.Description) {
		writeError(w, http.StatusBadRequest, "Invalid organization details")
		return
	}
	repositories, err := s.registry.Repositories(r.Context())
	if err != nil {
		s.registryError(w, err)
		return
	}
	item, err := s.organizations.Update(r.Context(), name, input.DisplayName, input.Description)
	if err != nil {
		s.organizationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, organizationResponse{Organization: item, RepositoryCount: repositoryCount(name, repositories)})
}

func (s *server) deleteOrganization(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !organization.ValidName(name) {
		writeError(w, http.StatusBadRequest, "Invalid organization name")
		return
	}
	repositories, err := s.registry.Repositories(r.Context())
	if err != nil {
		s.registryError(w, err)
		return
	}
	if repositoryCount(name, repositories) > 0 {
		writeError(w, http.StatusConflict, "Organization contains repositories")
		return
	}
	if err := s.organizations.Delete(r.Context(), name); err != nil {
		s.organizationError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "Invalid request body")
		return false
	}
	return true
}

func validOrganizationDetails(displayName, description string) bool {
	return displayName != "" && len(displayName) <= 100 && len(description) <= 500
}

func repositoryCount(name string, repositories []string) int {
	count := 0
	for _, repository := range repositories {
		if strings.HasPrefix(repository, name+"/") {
			count++
		}
	}
	return count
}

func (s *server) organizationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, organization.ErrNotFound):
		writeError(w, http.StatusNotFound, "Organization not found")
	case errors.Is(err, organization.ErrExists):
		writeError(w, http.StatusConflict, "Organization already exists")
	case errors.Is(err, organization.ErrOutcomeUnknown):
		slog.Warn("organization write outcome unknown", "error", err)
		writeError(w, http.StatusInternalServerError, "Write outcome unknown; reload the organization before retrying")
	default:
		slog.Warn("organization operation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "Organization storage failed")
	}
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	version, err := s.registry.Ping(r.Context())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"online": false, "registry": s.publicURL})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"online":     true,
		"registry":   s.publicURL,
		"apiVersion": version,
	})
}

func (s *server) repositories(w http.ResponseWriter, r *http.Request) {
	repositories, err := s.registry.Repositories(r.Context())
	if err != nil {
		s.registryError(w, err)
		return
	}
	if repositories == nil {
		repositories = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
}

func (s *server) repository(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
		defer s.invalidateCatalog(r.Context())
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/repositories/")
	switch {
	case strings.HasSuffix(rest, "/tags"):
		repository := strings.TrimSuffix(rest, "/tags")
		if !validRepository(repository) {
			writeError(w, http.StatusBadRequest, "Invalid repository name")
			return
		}
		switch r.Method {
		case http.MethodGet:
			s.images(w, r, repository)
		case http.MethodPost, http.MethodPut:
			s.putTag(w, r, repository)
		default:
			w.Header().Set("Allow", "GET, POST, PUT")
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case strings.Contains(rest, "/tags/"):
		index := strings.LastIndex(rest, "/tags/")
		repository, tag := rest[:index], rest[index+len("/tags/"):]
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		if !validRepository(repository) || !tagPattern.MatchString(tag) {
			writeError(w, http.StatusBadRequest, "Invalid repository or tag")
			return
		}
		if err := s.registry.DeleteTag(r.Context(), repository, tag); err != nil {
			s.registryError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case strings.Contains(rest, "/manifests/"):
		index := strings.LastIndex(rest, "/manifests/")
		repository, reference := rest[:index], rest[index+len("/manifests/"):]
		if !validRepository(repository) || reference == "" {
			writeError(w, http.StatusBadRequest, "Invalid repository or reference")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if !tagPattern.MatchString(reference) && !digestPattern.MatchString(reference) {
				writeError(w, http.StatusBadRequest, "Invalid manifest reference")
				return
			}
			image, _, _, err := s.registry.Manifest(r.Context(), repository, reference)
			if err != nil {
				s.registryError(w, err)
				return
			}
			image.Tag = reference
			writeJSON(w, http.StatusOK, image)
		case http.MethodDelete:
			if !digestPattern.MatchString(reference) {
				writeError(w, http.StatusBadRequest, "Manifest deletion requires a digest")
				return
			}
			if err := s.registry.DeleteManifest(r.Context(), repository, reference); err != nil {
				s.registryError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	default:
		writeError(w, http.StatusNotFound, "API endpoint not found")
	}
}

func (s *server) images(w http.ResponseWriter, r *http.Request, repository string) {
	images, err := s.repositoryImages(r.Context(), repository)
	if err != nil {
		s.registryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": repository, "images": images})
}

func (s *server) putTag(w http.ResponseWriter, r *http.Request, repository string) {
	var input struct {
		Source string `json:"source"`
		Target string `json:"target"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if !tagPattern.MatchString(input.Source) || !tagPattern.MatchString(input.Target) {
		writeError(w, http.StatusBadRequest, "Invalid source or target tag")
		return
	}
	image, err := s.registry.PutTag(r.Context(), repository, input.Source, input.Target)
	if err != nil {
		s.registryError(w, err)
		return
	}
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		status = http.StatusOK
	}
	writeJSON(w, status, image)
}

func (s *server) previewImageGC(w http.ResponseWriter, r *http.Request) {
	var input struct {
		UnusedForDays int `json:"unusedForDays"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	preview, err := s.retention.Preview(r.Context(), input.UnusedForDays)
	if err != nil {
		s.retentionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *server) executeImageGC(w http.ResponseWriter, r *http.Request) {
	defer s.invalidateCatalog(r.Context())
	var input struct {
		PlanID       string `json:"planId"`
		Confirmation string `json:"confirmation"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	if input.PlanID == "" {
		writeError(w, http.StatusBadRequest, "Plan ID is required")
		return
	}
	if input.Confirmation != "DELETE" {
		writeError(w, http.StatusBadRequest, "Confirmation must be DELETE")
		return
	}
	execution, err := s.retention.Execute(r.Context(), input.PlanID)
	if err != nil {
		s.retentionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, execution)
}

func (s *server) retentionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, retention.ErrInvalidUnusedForDays):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, retention.ErrCandidateLimit):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, retention.ErrPlanNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, retention.ErrPlanExpired):
		writeError(w, http.StatusGone, err.Error())
	case errors.Is(err, retention.ErrPlanUsed):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, retention.ErrUsage):
		slog.Warn("image cleanup storage operation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "Image usage storage failed")
	case errors.Is(err, retention.ErrRegistry):
		slog.Warn("image cleanup registry operation failed", "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		slog.Warn("image cleanup operation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "Image cleanup failed")
	}
}

func (s *server) registryError(w http.ResponseWriter, err error) {
	if errors.Is(err, errImageUsage) {
		writeError(w, http.StatusInternalServerError, errImageUsage.Error())
		return
	}
	status := http.StatusBadGateway
	var registryErr *registry.Error
	if errors.As(err, &registryErr) {
		switch registryErr.StatusCode {
		case http.StatusBadRequest:
			status = http.StatusBadRequest
		case http.StatusUnauthorized, http.StatusForbidden:
			status = http.StatusBadGateway
		case http.StatusNotFound:
			status = http.StatusNotFound
		case http.StatusMethodNotAllowed:
			status = http.StatusConflict
		}
	}
	slog.Warn("registry operation failed", "error", err, "status", status)
	writeError(w, status, err.Error())
}

func (s *server) web() http.Handler {
	root := os.DirFS(s.webRoot)
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
		if path == "." {
			path = "index.html"
		}
		if info, err := fs.Stat(root, path); err == nil && !info.IsDir() {
			if contentType := mime.TypeByExtension(filepath.Ext(path)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			files.ServeHTTP(w, r)
			return
		}
		index, err := fs.ReadFile(root, "index.html")
		if err != nil {
			writeError(w, http.StatusNotFound, "Web UI has not been built")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func validRepository(repository string) bool {
	return len(repository) < 256 && repositoryPattern.MatchString(repository)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Warn("write response", "error", err)
	}
}
