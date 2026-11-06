package proxycache

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	requests   *prometheus.CounterVec
	duration   *prometheus.HistogramVec
	inFlight   *prometheus.GaugeVec
	fallbacks  *prometheus.CounterVec
	backendUp  *prometheus.GaugeVec
	sourceInfo *prometheus.GaugeVec
}

func NewMetrics(registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mini_harbor_registry_requests_total",
			Help: "Total number of requests handled by the registry gateway.",
		}, []string{"route", "prefix", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mini_harbor_registry_request_duration_seconds",
			Help:    "Registry gateway request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		}, []string{"route", "prefix", "method"}),
		inFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mini_harbor_registry_requests_in_flight",
			Help: "Current number of registry gateway requests.",
		}, []string{"route", "prefix"}),
		fallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mini_harbor_registry_proxy_fallbacks_total",
			Help: "Total number of attempts that fell back to a lower-priority proxy source.",
		}, []string{"prefix", "upstream", "priority", "reason"}),
		backendUp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mini_harbor_registry_proxy_backend_up",
			Help: "Whether the local Distribution process for a proxy source is running.",
		}, []string{"prefix", "upstream", "priority"}),
		sourceInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mini_harbor_registry_proxy_source_info",
			Help: "Configured registry proxy sources.",
		}, []string{"prefix", "upstream", "priority"}),
	}
	registerer.MustRegister(
		metrics.requests,
		metrics.duration,
		metrics.inFlight,
		metrics.fallbacks,
		metrics.backendUp,
		metrics.sourceInfo,
	)
	return metrics
}

func (m *Metrics) beginRequest(route, prefix, method string) func(int) {
	if m == nil {
		return func(int) {}
	}
	method = metricMethod(method)
	started := time.Now()
	m.inFlight.WithLabelValues(route, prefix).Inc()
	return func(status int) {
		m.inFlight.WithLabelValues(route, prefix).Dec()
		m.requests.WithLabelValues(route, prefix, method, strconv.Itoa(status)).Inc()
		m.duration.WithLabelValues(route, prefix, method).Observe(time.Since(started).Seconds())
	}
}

func (m *Metrics) observeFallback(prefix, upstream string, priority int, reason string) {
	if m == nil {
		return
	}
	m.fallbacks.WithLabelValues(prefix, upstream, strconv.Itoa(priority), reason).Inc()
}

func (m *Metrics) addSource(prefix, upstream string, priority int) {
	if m == nil {
		return
	}
	labels := []string{prefix, upstream, strconv.Itoa(priority)}
	m.sourceInfo.WithLabelValues(labels...).Set(1)
}

func (m *Metrics) SetBackendUp(prefix, upstream string, priority int, up bool) {
	if m == nil {
		return
	}
	value := 0.0
	if up {
		value = 1
	}
	m.backendUp.WithLabelValues(prefix, upstream, strconv.Itoa(priority)).Set(value)
}

func metricMethod(method string) string {
	switch method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
		return method
	default:
		return "OTHER"
	}
}
