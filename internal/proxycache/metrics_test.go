package proxycache

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestGatewayRecordsMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	preferred := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer preferred.Close()
	fallback := httptest.NewServer(requestEcho("fallback"))
	defer fallback.Close()

	handler, err := NewGatewayWithMetrics(local.URL, []Route{
		{Prefix: "docker.io", Target: preferred.URL, Upstream: "https://mirror.example.com", Priority: 10},
		{Prefix: "docker.io", Target: fallback.URL, Upstream: "https://registry-1.docker.io", Priority: 100},
	}, metrics, nil)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://registry.example.test/v2/docker.io/library/alpine/manifests/latest", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	assertMetricValue(t, metrics.requests.WithLabelValues("proxy", "docker.io", "GET", "200"), 1)
	assertMetricValue(t, metrics.inFlight.WithLabelValues("proxy", "docker.io"), 0)
	assertMetricValue(t, metrics.fallbacks.WithLabelValues("docker.io", "https://mirror.example.com", "10", "503"), 1)
	assertMetricValue(t, metrics.sourceInfo.WithLabelValues("docker.io", "https://registry-1.docker.io", "100"), 1)

	metrics.SetBackendUp("docker.io", "https://registry-1.docker.io", 100, true)
	assertMetricValue(t, metrics.backendUp.WithLabelValues("docker.io", "https://registry-1.docker.io", "100"), 1)
}

func TestGatewayMetricsNormalizeUnknownMethods(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics(registry)
	local := httptest.NewServer(requestEcho("local"))
	defer local.Close()
	handler, err := NewGatewayWithMetrics(local.URL, nil, metrics, nil)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest("CUSTOM", "http://registry.example.test/v2/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertMetricValue(t, metrics.requests.WithLabelValues("local", "", "OTHER", "200"), 1)
}

func assertMetricValue(t *testing.T, collector prometheus.Collector, want float64) {
	t.Helper()
	if got := testutil.ToFloat64(collector); got != want {
		t.Fatalf("metric value = %v, want %v", got, want)
	}
}
