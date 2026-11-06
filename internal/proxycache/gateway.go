package proxycache

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Route struct {
	Prefix   string
	Target   string
	Upstream string
	Priority int
}

type fallbackStatusError int

func (e fallbackStatusError) Error() string {
	return fmt.Sprintf("registry source returned status %d", e)
}

type routeTarget struct {
	priority int
	target   *url.URL
	upstream string
}

type registryError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type UsageRecorder interface {
	Record(repository, digest string, usedAt time.Time) error
}

type gateway struct {
	local         *httputil.ReverseProxy
	routes        map[string]http.Handler
	metrics       *Metrics
	usageRecorder UsageRecorder
}

func NewGateway(localTarget string, routes []Route) (http.Handler, error) {
	return NewGatewayWithMetrics(localTarget, routes, nil, nil)
}

func NewGatewayWithMetrics(localTarget string, routes []Route, metrics *Metrics, usageRecorder UsageRecorder) (http.Handler, error) {
	localURL, err := parseTarget(localTarget)
	if err != nil {
		return nil, fmt.Errorf("invalid local registry target: %w", err)
	}

	gateway := &gateway{
		local:         reverseProxy(localURL, "", "", 0, nil, metrics),
		routes:        make(map[string]http.Handler, len(routes)),
		metrics:       metrics,
		usageRecorder: usageRecorder,
	}
	targets := make(map[string][]routeTarget, len(routes))
	for _, route := range routes {
		if !prefixPattern.MatchString(route.Prefix) {
			return nil, fmt.Errorf("invalid route prefix %q", route.Prefix)
		}
		target, err := parseTarget(route.Target)
		if err != nil {
			return nil, fmt.Errorf("invalid target for prefix %q: %w", route.Prefix, err)
		}
		upstream := route.Upstream
		if upstream == "" {
			upstream = route.Target
		}
		targets[route.Prefix] = append(targets[route.Prefix], routeTarget{
			priority: route.Priority,
			target:   target,
			upstream: upstream,
		})
		metrics.addSource(route.Prefix, upstream, route.Priority)
	}
	for prefix, prefixTargets := range targets {
		sort.SliceStable(prefixTargets, func(i, j int) bool {
			return prefixTargets[i].priority < prefixTargets[j].priority
		})
		for i := 1; i < len(prefixTargets); i++ {
			if prefixTargets[i-1].priority == prefixTargets[i].priority {
				return nil, fmt.Errorf("prefix %q has duplicate priority %d", prefix, prefixTargets[i].priority)
			}
		}

		var handler http.Handler
		for i := len(prefixTargets) - 1; i >= 0; i-- {
			target := prefixTargets[i]
			handler = reverseProxy(target.target, prefix, target.upstream, target.priority, handler, metrics)
		}
		gateway.routes[prefix] = handler
	}
	return gateway, nil
}

func (g *gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := requestPrefix(r.URL.Path)
	proxy, matched := g.routes[prefix]
	route := "proxy"
	if !matched {
		route = "local"
		prefix = ""
		proxy = g.local
	}
	observed := &statusResponseWriter{ResponseWriter: w, status: http.StatusOK}
	finish := g.metrics.beginRequest(route, prefix, r.Method)
	defer func() { finish(observed.status) }()
	if matched && r.Method != http.MethodGet && r.Method != http.MethodHead {
		observed.Header().Set("Allow", "GET, HEAD")
		writeRegistryError(observed, http.StatusMethodNotAllowed, "UNSUPPORTED", "proxy namespaces are read-only")
		return
	}
	proxy.ServeHTTP(observed, r)
	if g.usageRecorder == nil {
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return
	}
	if observed.status < 200 || observed.status >= 300 {
		return
	}
	repository, ok := manifestRepository(r.URL.Path)
	digest := observed.Header().Get("Docker-Content-Digest")
	if !ok || digest == "" {
		return
	}
	if err := g.usageRecorder.Record(repository, digest, time.Now()); err != nil {
		slog.Warn("record image usage", "repository", repository, "digest", digest, "error", err)
	}
}

func parseTarget(rawURL string) (*url.URL, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		return nil, fmt.Errorf("invalid registry URL %q", rawURL)
	}
	return target, nil
}

func reverseProxy(target *url.URL, prefix, upstream string, priority int, fallback http.Handler, metrics *Metrics) *httputil.ReverseProxy {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			forwardedProto := request.In.Header.Get("X-Forwarded-Proto")
			forwardedHost := request.In.Header.Get("X-Forwarded-Host")
			request.SetURL(target)
			request.Out.Host = request.In.Host
			request.SetXForwarded()
			if forwardedProto != "" {
				request.Out.Header.Set("X-Forwarded-Proto", forwardedProto)
			}
			if forwardedHost != "" {
				request.Out.Header.Set("X-Forwarded-Host", forwardedHost)
			}
			if prefix != "" {
				prefixPath := "/v2/" + prefix
				if request.In.URL.Path == prefixPath {
					request.Out.URL.Path = "/v2"
				} else if strings.HasPrefix(request.In.URL.Path, prefixPath+"/") {
					request.Out.URL.Path = "/v2" + strings.TrimPrefix(request.In.URL.Path, prefixPath)
				}
				request.Out.URL.RawPath = ""
			}
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Set("Docker-Distribution-API-Version", "registry/2.0")
			if fallback != nil && shouldFallback(response.StatusCode) {
				return fallbackStatusError(response.StatusCode)
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if fallback != nil {
				reason := "error"
				if status, ok := err.(fallbackStatusError); ok {
					reason = strconv.Itoa(int(status))
				}
				metrics.observeFallback(prefix, upstream, priority, reason)
				slog.Warn("registry source failed; trying lower-priority source", "upstream", upstream, "priority", priority, "error", err)
				fallback.ServeHTTP(w, r)
				return
			}
			slog.Warn("registry backend unavailable", "error", err)
			writeRegistryError(w, http.StatusBadGateway, "UNAVAILABLE", "registry backend unavailable")
		},
	}
	return proxy
}

type statusResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

func (w *statusResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func shouldFallback(status int) bool {
	return status == http.StatusNotFound || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError
}

func requestPrefix(path string) string {
	if !strings.HasPrefix(path, "/v2/") {
		return ""
	}
	repository := strings.TrimPrefix(path, "/v2/")
	if slash := strings.IndexByte(repository, '/'); slash >= 0 {
		return repository[:slash]
	}
	return repository
}

func manifestRepository(path string) (string, bool) {
	if !strings.HasPrefix(path, "/v2/") {
		return "", false
	}
	rest := strings.TrimPrefix(path, "/v2/")
	index := strings.LastIndex(rest, "/manifests/")
	if index <= 0 || index+len("/manifests/") == len(rest) {
		return "", false
	}
	return rest[:index], true
}

func writeRegistryError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Errors []registryError `json:"errors"`
	}{Errors: []registryError{{Code: code, Message: message}}})
}
