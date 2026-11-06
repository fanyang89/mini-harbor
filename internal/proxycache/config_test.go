package proxycache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("DOCKER_PROXY_PASSWORD", "secret")
	path := filepath.Join(t.TempDir(), "proxies.yml")
	data := []byte(`proxies:
  - prefix: docker.io
    remote_url: https://registry-1.docker.io/
    priority: 10
    username: mirror-user
    password: ${DOCKER_PROXY_PASSWORD}
  - prefix: docker.io
    remote_url: https://docker.example.com
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Proxies) != 2 {
		t.Fatalf("got %d proxies", len(config.Proxies))
	}
	proxy := config.Proxies[0]
	if proxy.Prefix != "docker.io" || proxy.RemoteURL != "https://registry-1.docker.io" {
		t.Fatalf("unexpected proxy: %#v", proxy)
	}
	if proxy.Password != "secret" || proxy.Priority != 10 || proxy.TTL != defaultTTL {
		t.Fatalf("environment or defaults not applied: %#v", proxy)
	}
	if config.Proxies[1].Priority != defaultPriority {
		t.Fatalf("default priority = %d, want %d", config.Proxies[1].Priority, defaultPriority)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		message string
	}{
		{
			name:    "invalid prefix",
			config:  Config{Proxies: []Upstream{{Prefix: "GHCR.IO", RemoteURL: "https://ghcr.io"}}},
			message: "invalid prefix",
		},
		{
			name: "duplicate priority",
			config: Config{Proxies: []Upstream{
				{Prefix: "ghcr.io", RemoteURL: "https://mirror.example.com", Priority: 10},
				{Prefix: "ghcr.io", RemoteURL: "https://ghcr.io", Priority: 10},
			}},
			message: "duplicate priority",
		},
		{
			name: "duplicate remote URL",
			config: Config{Proxies: []Upstream{
				{Prefix: "ghcr.io", RemoteURL: "https://ghcr.io", Priority: 10},
				{Prefix: "ghcr.io", RemoteURL: "https://ghcr.io", Priority: 20},
			}},
			message: "duplicate remote_url",
		},
		{
			name:    "invalid priority",
			config:  Config{Proxies: []Upstream{{Prefix: "quay.io", RemoteURL: "https://quay.io", Priority: -1}}},
			message: "invalid priority",
		},
		{
			name:    "remote path",
			config:  Config{Proxies: []Upstream{{Prefix: "quay.io", RemoteURL: "https://quay.io/team"}}},
			message: "registry root URL",
		},
		{
			name:    "partial credentials",
			config:  Config{Proxies: []Upstream{{Prefix: "quay.io", RemoteURL: "https://quay.io", Username: "user"}}},
			message: "both username and password",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.validate()
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v, want error containing %q", err, test.message)
			}
		})
	}
}

func TestDistributionConfig(t *testing.T) {
	data, err := DistributionConfig(Upstream{
		Prefix:    "quay.io",
		RemoteURL: "https://quay.io",
		Priority:  10,
		TTL:       "24h",
	}, "127.0.0.1:5101", "/cache/quay.io", "http-secret")
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, expected := range []string{
		"rootdirectory: /cache/quay.io",
		"addr: 127.0.0.1:5101",
		"remoteurl: https://quay.io",
		"upstream: https://quay.io",
		"priority: \"10\"",
		"ttl: 24h",
	} {
		if !strings.Contains(config, expected) {
			t.Fatalf("generated config missing %q:\n%s", expected, config)
		}
	}
}
