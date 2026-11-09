package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/proxycache"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type backend struct {
	prefix     string
	upstream   string
	priority   int
	configPath string
}

func main() {
	if err := run(); err != nil {
		slog.Error("registry gateway stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := proxycache.Load(getenv("PROXY_CONFIG_PATH", "/etc/mini-harbor/proxies.yml"))
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	metrics := proxycache.NewMetrics(prometheus.DefaultRegisterer)
	routes, cleanup, err := startBackends(
		ctx,
		config,
		getenv("REGISTRY_BINARY", "/usr/local/bin/registry"),
		getenv("PROXY_STORAGE_ROOT", "/var/lib/registry-proxy"),
		getenv("REGISTRY_HTTP_SECRET", "mini-harbor-local-development-secret"),
		metrics,
	)
	if err != nil {
		return err
	}
	defer cleanup()

	databaseContext, cancelDatabase := context.WithTimeout(ctx, 15*time.Second)
	usageStore, err := organization.Open(databaseContext, strings.Split(getenv("RQLITE_URLS", "http://rqlite:4001"), ",")...)
	cancelDatabase()
	if err != nil {
		return err
	}
	defer usageStore.Close()
	handler, err := proxycache.NewGatewayWithMetrics(getenv("LOCAL_REGISTRY_URL", "http://registry:5000"), routes, metrics, usageStore)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.Handle("/", handler)
	server := &http.Server{
		Addr:              getenv("LISTEN_ADDR", ":5000"),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ListenAndServe()
	}()

	slog.Info("registry gateway started", "address", server.Addr, "proxies", len(routes))
	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}

	shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownContext); err != nil && runErr == nil {
		runErr = err
	}
	return runErr
}

func startBackends(ctx context.Context, config proxycache.Config, registryBinary, storageRoot, secret string, metrics *proxycache.Metrics) ([]proxycache.Route, func(), error) {
	if err := os.MkdirAll(storageRoot, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create proxy storage root: %w", err)
	}
	configDirectory, err := os.MkdirTemp("", "mini-harbor-proxies-")
	if err != nil {
		return nil, nil, fmt.Errorf("create proxy config directory: %w", err)
	}

	var backends []backend
	var routes []proxycache.Route
	sourceCounts := make(map[string]int, len(config.Proxies))
	for _, upstream := range config.Proxies {
		sourceCounts[upstream.Prefix]++
	}
	for i, upstream := range config.Proxies {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			_ = os.RemoveAll(configDirectory)
			return nil, nil, fmt.Errorf("allocate address for proxy %q: %w", upstream.Prefix, err)
		}
		address := listener.Addr().String()
		_ = listener.Close()

		sourceHash := fmt.Sprintf("%x", sha256.Sum256([]byte(upstream.RemoteURL)))[:12]
		storagePath := filepath.Join(storageRoot, upstream.Prefix+"-"+sourceHash)
		if sourceCounts[upstream.Prefix] == 1 {
			legacyPath := filepath.Join(storageRoot, upstream.Prefix)
			if err := migrateLegacyCache(legacyPath, storagePath); err != nil {
				_ = os.RemoveAll(configDirectory)
				return nil, nil, fmt.Errorf("migrate cache for proxy %q: %w", upstream.Prefix, err)
			}
		}
		data, err := proxycache.DistributionConfig(upstream, address, storagePath, secret)
		if err != nil {
			_ = os.RemoveAll(configDirectory)
			return nil, nil, fmt.Errorf("build config for proxy %q: %w", upstream.Prefix, err)
		}
		configPath := filepath.Join(configDirectory, fmt.Sprintf("proxy-%d.yml", i))
		if err := os.WriteFile(configPath, data, 0o600); err != nil {
			_ = os.RemoveAll(configDirectory)
			return nil, nil, fmt.Errorf("write config for proxy %q: %w", upstream.Prefix, err)
		}

		backends = append(backends, backend{
			prefix:     upstream.Prefix,
			upstream:   upstream.RemoteURL,
			priority:   upstream.Priority,
			configPath: configPath,
		})
		metrics.SetBackendUp(upstream.Prefix, upstream.RemoteURL, upstream.Priority, false)
		routes = append(routes, proxycache.Route{
			Prefix:   upstream.Prefix,
			Target:   "http://" + address,
			Upstream: upstream.RemoteURL,
			Priority: upstream.Priority,
		})
	}

	backendContext, stopBackends := context.WithCancel(ctx)
	var workers sync.WaitGroup
	for _, item := range backends {
		workers.Add(1)
		go func() {
			defer workers.Done()
			superviseBackend(backendContext, registryBinary, item, metrics)
		}()
	}
	cleanup := func() {
		stopBackends()
		workers.Wait()
		_ = os.RemoveAll(configDirectory)
	}
	return routes, cleanup, nil
}

func migrateLegacyCache(legacyPath, sourcePath string) error {
	if _, err := os.Stat(sourcePath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := os.Stat(legacyPath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := os.Rename(legacyPath, sourcePath); err != nil {
		return err
	}
	slog.Info("migrated legacy proxy cache", "source", sourcePath)
	return nil
}

func childEnvironment() []string {
	environment := os.Environ()
	filtered := environment[:0]
	for _, entry := range environment {
		if strings.HasPrefix(entry, "REGISTRY_URL=") || strings.HasPrefix(entry, "REGISTRY_PUBLIC_URL=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func superviseBackend(ctx context.Context, registryBinary string, item backend, metrics *proxycache.Metrics) {
	for {
		command := exec.CommandContext(ctx, registryBinary, "serve", item.configPath)
		command.Cancel = func() error {
			return command.Process.Signal(syscall.SIGTERM)
		}
		command.WaitDelay = 5 * time.Second
		command.Env = childEnvironment()
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		err := command.Start()
		if err == nil {
			metrics.SetBackendUp(item.prefix, item.upstream, item.priority, true)
			err = command.Wait()
		}
		metrics.SetBackendUp(item.prefix, item.upstream, item.priority, false)
		if ctx.Err() != nil {
			return
		}
		slog.Error("proxy registry stopped; restarting", "prefix", item.prefix, "upstream", item.upstream, "priority", item.priority, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
