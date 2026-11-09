package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fanyang89/mini-harbor/internal/auth"
	"github.com/fanyang89/mini-harbor/internal/httpapi"
	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/registry"
	"github.com/fanyang89/mini-harbor/internal/usage"
)

func main() {
	registryURL := getenv("REGISTRY_URL", "http://localhost:5000")
	publicURL := getenv("REGISTRY_PUBLIC_URL", "localhost:5000")
	listenAddr := getenv("LISTEN_ADDR", ":8080")
	webRoot := getenv("WEB_ROOT", "web/dist")
	databaseURLs := splitCSV(getenv("RQLITE_URLS", "http://localhost:4001"))
	imageUsageRoot := getenv("IMAGE_USAGE_ROOT", "data/image-usage")
	redirectURL := os.Getenv("OIDC_REDIRECT_URL")
	cookieSecure, err := strconv.ParseBool(getenv("OIDC_COOKIE_SECURE", strconv.FormatBool(strings.HasPrefix(redirectURL, "https://"))))
	if err != nil {
		slog.Error("invalid OIDC_COOKIE_SECURE", "error", err)
		os.Exit(1)
	}

	client, err := registry.NewClient(registryURL, &http.Client{Timeout: 20 * time.Second})
	if err != nil {
		slog.Error("invalid registry configuration", "error", err)
		os.Exit(1)
	}
	databaseContext, cancelDatabase := context.WithTimeout(context.Background(), 15*time.Second)
	organizations, err := organization.Open(databaseContext, databaseURLs...)
	cancelDatabase()
	if err != nil {
		slog.Error("open organization database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := organizations.Close(); err != nil {
			slog.Warn("close organization database", "error", err)
		}
	}()
	usageStore, err := usage.Open(imageUsageRoot)
	if err != nil {
		slog.Error("open image usage store", "error", err)
		os.Exit(1)
	}
	discoveryContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	authentication, err := auth.New(discoveryContext, auth.Config{
		Issuer:        os.Getenv("OIDC_ISSUER"),
		ClientID:      os.Getenv("OIDC_CLIENT_ID"),
		ClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
		RedirectURL:   redirectURL,
		Scopes:        splitCSV(os.Getenv("OIDC_SCOPES")),
		GroupsClaim:   getenv("OIDC_GROUPS_CLAIM", "groups"),
		AllowedGroups: splitCSV(os.Getenv("OIDC_ALLOWED_GROUPS")),
		SessionSecret: []byte(os.Getenv("OIDC_SESSION_SECRET")),
		CookieSecure:  cookieSecure,
	})
	if err != nil {
		slog.Error("invalid OIDC configuration", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           httpapi.New(client, organizations, usageStore, publicURL, webRoot, authentication),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	slog.Info("mini-harbor started", "address", listenAddr, "registry", registryURL, "oidc", authentication.Enabled())
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func splitCSV(value string) []string {
	var values []string
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			values = append(values, part)
		}
	}
	return values
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
