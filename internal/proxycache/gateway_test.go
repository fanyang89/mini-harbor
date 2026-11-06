package proxycache

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGatewayRoutesProxyPrefix(t *testing.T) {
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	remote := httptest.NewServer(requestEcho("remote"))
	defer remote.Close()
	secondRemote := httptest.NewServer(requestEcho("second"))
	defer secondRemote.Close()

	handler, err := NewGateway(local.URL, []Route{
		{Prefix: "docker.io", Target: remote.URL, Priority: 10},
		{Prefix: "ghcr.io", Target: secondRemote.URL, Priority: 10},
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		want string
	}{
		{path: "/v2/", want: "local GET /v2/ host=registry.example.test"},
		{path: "/v2/team/app/manifests/latest", want: "local GET /v2/team/app/manifests/latest host=registry.example.test"},
		{path: "/v2/docker.io", want: "remote GET /v2 host=registry.example.test"},
		{path: "/v2/docker.io/library/alpine/manifests/latest?source=test", want: "remote GET /v2/library/alpine/manifests/latest?source=test host=registry.example.test"},
		{path: "/v2/ghcr.io/example/app/blobs/sha256:123", want: "second GET /v2/example/app/blobs/sha256:123 host=registry.example.test"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, "http://registry.example.test"+test.path, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if body := strings.TrimSpace(response.Body.String()); body != test.want {
			t.Errorf("%s: got %q, want %q", test.path, body, test.want)
		}
		if got := response.Header().Get("Docker-Distribution-API-Version"); got != "registry/2.0" {
			t.Errorf("API version header = %q", got)
		}
	}
}

func TestGatewayUsesSourcePriority(t *testing.T) {
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	preferred := httptest.NewServer(requestEcho("preferred"))
	defer preferred.Close()
	fallback := httptest.NewServer(requestEcho("fallback"))
	defer fallback.Close()

	handler, err := NewGateway(local.URL, []Route{
		{Prefix: "docker.io", Target: fallback.URL, Priority: 100},
		{Prefix: "docker.io", Target: preferred.URL, Priority: 10},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/docker.io/library/alpine/manifests/latest", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if body := strings.TrimSpace(response.Body.String()); body != "preferred GET /v2/library/alpine/manifests/latest host=registry.example.test" {
		t.Fatalf("unexpected response: %q", body)
	}
}

func TestGatewayPreservesForwardedOrigin(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "%s %s", r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Host"))
	}))
	defer local.Close()
	handler, err := NewGateway(local.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/", nil)
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "registry.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if body := strings.TrimSpace(response.Body.String()); body != "https registry.example.test" {
		t.Fatalf("forwarded origin = %q", body)
	}
}

func TestGatewayFallsBackToNextSource(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			local := httptest.NewServer(requestEcho("local"))
			defer local.Close()
			preferred := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
			}))
			defer preferred.Close()
			fallback := httptest.NewServer(requestEcho("fallback"))
			defer fallback.Close()

			handler, err := NewGateway(local.URL, []Route{
				{Prefix: "docker.io", Target: preferred.URL, Priority: 10},
				{Prefix: "docker.io", Target: fallback.URL, Priority: 20},
			})
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/docker.io/library/alpine/manifests/latest", nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if body := strings.TrimSpace(response.Body.String()); body != "fallback GET /v2/library/alpine/manifests/latest host=registry.example.test" {
				t.Fatalf("unexpected response: %q", body)
			}
		})
	}
}

func TestGatewayFallsBackFromUnavailableSource(t *testing.T) {
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	fallback := httptest.NewServer(requestEcho("fallback"))
	defer fallback.Close()
	handler, err := NewGateway(local.URL, []Route{
		{Prefix: "docker.io", Target: "http://127.0.0.1:1", Priority: 10},
		{Prefix: "docker.io", Target: fallback.URL, Priority: 20},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/docker.io/library/alpine/manifests/latest", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if body := strings.TrimSpace(response.Body.String()); !strings.HasPrefix(body, "fallback GET") {
		t.Fatalf("unexpected response: %q", body)
	}
}

func TestGatewayRejectsDuplicatePriorities(t *testing.T) {
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	_, err := NewGateway(local.URL, []Route{
		{Prefix: "docker.io", Target: "http://127.0.0.1:5101", Priority: 10},
		{Prefix: "docker.io", Target: "http://127.0.0.1:5102", Priority: 10},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate priority") {
		t.Fatalf("got %v, want duplicate priority error", err)
	}
}

func TestGatewayRejectsProxyWrites(t *testing.T) {
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	remote := httptest.NewServer(requestEcho("remote"))
	defer remote.Close()
	handler, err := NewGateway(local.URL, []Route{{Prefix: "docker.io", Target: remote.URL}})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, "http://registry.example.test/v2/docker.io/library/alpine/manifests/latest", strings.NewReader("manifest"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if !strings.Contains(response.Body.String(), `"code":"UNSUPPORTED"`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestGatewayReportsUnavailableBackend(t *testing.T) {
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	handler, err := NewGateway(local.URL, []Route{{Prefix: "docker.io", Target: "http://127.0.0.1:1"}})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/docker.io/library/alpine/manifests/latest", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	if !strings.Contains(response.Body.String(), `"code":"UNAVAILABLE"`) {
		t.Fatalf("unexpected response: %s", response.Body.String())
	}
}

func TestGatewayRecordsSuccessfulManifestPulls(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "missing") {
			w.Header().Set("Docker-Content-Digest", "sha256:missing")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:abcd")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	recorder := &recordedUsage{}
	handler, err := NewGatewayWithMetrics(backend.URL, []Route{{Prefix: "docker.io", Target: backend.URL}}, nil, recorder)
	if err != nil {
		t.Fatal(err)
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		request := httptest.NewRequest(method, "http://registry.example.test/v2/team/app/manifests/latest", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
	}
	if len(recorder.records) != 2 || recorder.records[0].repository != "team/app" || recorder.records[0].digest != "sha256:abcd" || recorder.records[0].usedAt.IsZero() {
		t.Fatalf("unexpected usage records: %#v", recorder.records)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/docker.io/team/app/manifests/latest", nil))
	if len(recorder.records) != 3 || recorder.records[2].repository != "docker.io/team/app" {
		t.Fatalf("proxy pull usage records: %#v", recorder.records)
	}

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/team/app/manifests/missing", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/team/app/blobs/sha256:abcd", nil))
	if len(recorder.records) != 3 {
		t.Fatalf("non-pull requests recorded usage: %#v", recorder.records)
	}
}

type usageRecord struct {
	repository string
	digest     string
	usedAt     time.Time
}

type recordedUsage struct {
	records []usageRecord
}

func (r *recordedUsage) Record(repository, digest string, usedAt time.Time) error {
	r.records = append(r.records, usageRecord{repository: repository, digest: digest, usedAt: usedAt})
	return nil
}

func requestEcho(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, fmt.Sprintf("%s %s %s host=%s", name, r.Method, r.URL.RequestURI(), r.Host))
	})
}
