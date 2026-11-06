package proxycache

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	defaultPriority = 100
	defaultTTL      = "168h"
)

var prefixPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

type Config struct {
	Proxies []Upstream `yaml:"proxies"`
}

type Upstream struct {
	Prefix    string `yaml:"prefix"`
	RemoteURL string `yaml:"remote_url"`
	Priority  int    `yaml:"priority,omitempty"`
	Username  string `yaml:"username,omitempty"`
	Password  string `yaml:"password,omitempty"`
	TTL       string `yaml:"ttl,omitempty"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read proxy config: %w", err)
	}

	var config Config
	decoder := yaml.NewDecoder(strings.NewReader(os.ExpandEnv(string(data))))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode proxy config: %w", err)
	}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c *Config) validate() error {
	priorities := make(map[string]map[int]struct{}, len(c.Proxies))
	remoteURLs := make(map[string]map[string]struct{}, len(c.Proxies))
	for i := range c.Proxies {
		proxy := &c.Proxies[i]
		proxy.Prefix = strings.TrimSpace(proxy.Prefix)
		proxy.RemoteURL = strings.TrimSpace(proxy.RemoteURL)
		proxy.TTL = strings.TrimSpace(proxy.TTL)
		if proxy.Priority == 0 {
			proxy.Priority = defaultPriority
		}
		if proxy.TTL == "" {
			proxy.TTL = defaultTTL
		}

		if !prefixPattern.MatchString(proxy.Prefix) {
			return fmt.Errorf("proxy %d has invalid prefix %q", i+1, proxy.Prefix)
		}
		if proxy.Priority < 1 {
			return fmt.Errorf("proxy %q has invalid priority %d", proxy.Prefix, proxy.Priority)
		}
		if priorities[proxy.Prefix] == nil {
			priorities[proxy.Prefix] = make(map[int]struct{})
			remoteURLs[proxy.Prefix] = make(map[string]struct{})
		}
		if _, exists := priorities[proxy.Prefix][proxy.Priority]; exists {
			return fmt.Errorf("proxy %q has duplicate priority %d", proxy.Prefix, proxy.Priority)
		}
		priorities[proxy.Prefix][proxy.Priority] = struct{}{}

		remote, err := url.Parse(proxy.RemoteURL)
		if err != nil || remote.Host == "" || (remote.Scheme != "http" && remote.Scheme != "https") {
			return fmt.Errorf("proxy %q has invalid remote_url %q", proxy.Prefix, proxy.RemoteURL)
		}
		if remote.User != nil || (remote.Path != "" && remote.Path != "/") || remote.RawQuery != "" || remote.Fragment != "" {
			return fmt.Errorf("proxy %q remote_url must be a registry root URL", proxy.Prefix)
		}
		proxy.RemoteURL = strings.TrimRight(proxy.RemoteURL, "/")
		if _, exists := remoteURLs[proxy.Prefix][proxy.RemoteURL]; exists {
			return fmt.Errorf("proxy %q has duplicate remote_url %q", proxy.Prefix, proxy.RemoteURL)
		}
		remoteURLs[proxy.Prefix][proxy.RemoteURL] = struct{}{}

		if _, err := time.ParseDuration(proxy.TTL); err != nil {
			return fmt.Errorf("proxy %q has invalid ttl: %w", proxy.Prefix, err)
		}
		if (proxy.Username == "") != (proxy.Password == "") {
			return fmt.Errorf("proxy %q must set both username and password", proxy.Prefix)
		}
	}
	return nil
}

func DistributionConfig(upstream Upstream, address, storageRoot, secret string) ([]byte, error) {
	type cacheConfig struct {
		BlobDescriptor string `yaml:"blobdescriptor"`
	}
	type filesystemConfig struct {
		RootDirectory string `yaml:"rootdirectory"`
	}
	type deleteConfig struct {
		Enabled bool `yaml:"enabled"`
	}
	type storageConfig struct {
		Cache      cacheConfig      `yaml:"cache"`
		Filesystem filesystemConfig `yaml:"filesystem"`
		Delete     deleteConfig     `yaml:"delete"`
	}
	type logConfig struct {
		Level     string            `yaml:"level"`
		Formatter string            `yaml:"formatter"`
		Fields    map[string]string `yaml:"fields"`
	}
	type httpConfig struct {
		Addr    string              `yaml:"addr"`
		Secret  string              `yaml:"secret"`
		Headers map[string][]string `yaml:"headers"`
	}
	type proxyConfig struct {
		RemoteURL string `yaml:"remoteurl"`
		Username  string `yaml:"username,omitempty"`
		Password  string `yaml:"password,omitempty"`
		TTL       string `yaml:"ttl"`
	}
	type distributionConfig struct {
		Version string        `yaml:"version"`
		Log     logConfig     `yaml:"log"`
		Storage storageConfig `yaml:"storage"`
		HTTP    httpConfig    `yaml:"http"`
		Proxy   proxyConfig   `yaml:"proxy"`
	}

	return yaml.Marshal(distributionConfig{
		Version: "0.1",
		Log: logConfig{
			Level:     "info",
			Formatter: "json",
			Fields: map[string]string{
				"service":  "registry-proxy",
				"prefix":   upstream.Prefix,
				"upstream": upstream.RemoteURL,
				"priority": fmt.Sprintf("%d", upstream.Priority),
			},
		},
		Storage: storageConfig{
			Cache:      cacheConfig{BlobDescriptor: "inmemory"},
			Filesystem: filesystemConfig{RootDirectory: storageRoot},
			Delete:     deleteConfig{Enabled: true},
		},
		HTTP: httpConfig{
			Addr:    address,
			Secret:  secret,
			Headers: map[string][]string{"X-Content-Type-Options": {"nosniff"}},
		},
		Proxy: proxyConfig{
			RemoteURL: upstream.RemoteURL,
			Username:  upstream.Username,
			Password:  upstream.Password,
			TTL:       upstream.TTL,
		},
	})
}
