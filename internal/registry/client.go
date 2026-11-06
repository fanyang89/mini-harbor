package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const (
	pageSize        = 1000
	maxResponseSize = 16 << 20
	maxConfigSize   = 2 << 20
	manifestAccept  = "application/vnd.oci.image.index.v1+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json"
)

type Client struct {
	baseURL *url.URL
	http    *http.Client
}

type Descriptor struct {
	MediaType string    `json:"mediaType"`
	Size      int64     `json:"size"`
	Digest    string    `json:"digest"`
	Platform  *Platform `json:"platform,omitempty"`
}

type Platform struct {
	Architecture string `json:"architecture,omitempty"`
	OS           string `json:"os,omitempty"`
	Variant      string `json:"variant,omitempty"`
}

type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        Descriptor   `json:"config"`
	Layers        []Descriptor `json:"layers"`
	Manifests     []Descriptor `json:"manifests"`
}

type ImageConfig struct {
	Created      string `json:"created,omitempty"`
	Architecture string `json:"architecture,omitempty"`
	OS           string `json:"os,omitempty"`
}

type Image struct {
	Tag          string     `json:"tag"`
	Digest       string     `json:"digest"`
	MediaType    string     `json:"mediaType"`
	Size         int64      `json:"size"`
	Layers       int        `json:"layers"`
	Platforms    []Platform `json:"platforms,omitempty"`
	Created      string     `json:"created,omitempty"`
	LastUsed     string     `json:"lastUsed,omitempty"`
	Architecture string     `json:"architecture,omitempty"`
	OS           string     `json:"os,omitempty"`
}

type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return e.Message
}

func NewClient(rawURL string, httpClient *http.Client) (*Client, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil, fmt.Errorf("invalid registry URL %q", rawURL)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported registry URL scheme %q", baseURL.Scheme)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, http: httpClient}, nil
}

func (c *Client) Ping(ctx context.Context) (string, error) {
	response, err := c.request(ctx, http.MethodGet, "/v2/", "", nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	return response.Header.Get("Docker-Distribution-API-Version"), nil
}

func (c *Client) Repositories(ctx context.Context) ([]string, error) {
	var repositories []string
	last := ""
	for {
		path := "/v2/_catalog?n=" + strconv.Itoa(pageSize)
		if last != "" {
			path += "&last=" + url.QueryEscape(last)
		}
		var page struct {
			Repositories []string `json:"repositories"`
		}
		hasNext, err := c.getPage(ctx, path, &page)
		if err != nil {
			return nil, err
		}
		repositories = append(repositories, page.Repositories...)
		if !hasNext || len(page.Repositories) == 0 {
			return repositories, nil
		}
		last = page.Repositories[len(page.Repositories)-1]
	}
}

func (c *Client) Tags(ctx context.Context, repository string) ([]string, error) {
	var tags []string
	last := ""
	for {
		path := "/v2/" + escapeRepository(repository) + "/tags/list?n=" + strconv.Itoa(pageSize)
		if last != "" {
			path += "&last=" + url.QueryEscape(last)
		}
		var page struct {
			Tags []string `json:"tags"`
		}
		hasNext, err := c.getPage(ctx, path, &page)
		if err != nil {
			return nil, err
		}
		tags = append(tags, page.Tags...)
		if !hasNext || len(page.Tags) == 0 {
			return tags, nil
		}
		last = page.Tags[len(page.Tags)-1]
	}
}

func (c *Client) Images(ctx context.Context, repository string) ([]Image, error) {
	tags, err := c.Tags(ctx, repository)
	if err != nil {
		return nil, err
	}

	images := make([]Image, len(tags))
	errByIndex := make([]error, len(tags))
	jobs := make(chan int)
	var workers sync.WaitGroup
	workerCount := min(8, len(tags))
	for range workerCount {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				images[i], _, _, errByIndex[i] = c.Manifest(ctx, repository, tags[i])
				images[i].Tag = tags[i]
			}
		}()
	}
	for i := range tags {
		jobs <- i
	}
	close(jobs)
	workers.Wait()

	for _, err := range errByIndex {
		if err != nil {
			return nil, err
		}
	}
	return images, nil
}

func (c *Client) Manifest(ctx context.Context, repository, reference string) (Image, []byte, string, error) {
	path := "/v2/" + escapeRepository(repository) + "/manifests/" + reference
	response, err := c.request(ctx, http.MethodGet, path, manifestAccept, nil)
	if err != nil {
		return Image{}, nil, "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return Image{}, nil, "", fmt.Errorf("read manifest: %w", err)
	}
	if len(body) > maxResponseSize {
		return Image{}, nil, "", errors.New("manifest exceeds size limit")
	}

	contentType := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return Image{}, nil, "", fmt.Errorf("decode manifest: %w", err)
	}
	if manifest.MediaType == "" {
		manifest.MediaType = contentType
	}

	image := Image{
		Digest:    response.Header.Get("Docker-Content-Digest"),
		MediaType: manifest.MediaType,
		Layers:    len(manifest.Layers),
	}
	for _, layer := range manifest.Layers {
		image.Size += layer.Size
	}
	image.Size += manifest.Config.Size
	for _, item := range manifest.Manifests {
		image.Size += item.Size
		if item.Platform != nil {
			image.Platforms = append(image.Platforms, *item.Platform)
		}
	}

	if manifest.Config.Digest != "" {
		config, configErr := c.imageConfig(ctx, repository, manifest.Config.Digest)
		if configErr == nil {
			image.Created = config.Created
			image.Architecture = config.Architecture
			image.OS = config.OS
		}
	}
	return image, body, contentType, nil
}

func (c *Client) PutTag(ctx context.Context, repository, source, target string) (Image, error) {
	image, manifest, contentType, err := c.Manifest(ctx, repository, source)
	if err != nil {
		return Image{}, err
	}
	path := "/v2/" + escapeRepository(repository) + "/manifests/" + target
	response, err := c.request(ctx, http.MethodPut, path, contentType, bytes.NewReader(manifest))
	if err != nil {
		return Image{}, err
	}
	response.Body.Close()
	image.Tag = target
	if digest := response.Header.Get("Docker-Content-Digest"); digest != "" {
		image.Digest = digest
	}
	return image, nil
}

func (c *Client) DeleteTag(ctx context.Context, repository, tag string) error {
	return c.delete(ctx, "/v2/"+escapeRepository(repository)+"/manifests/"+tag)
}

func (c *Client) DeleteManifest(ctx context.Context, repository, digest string) error {
	return c.delete(ctx, "/v2/"+escapeRepository(repository)+"/manifests/"+digest)
}

func (c *Client) delete(ctx context.Context, path string) error {
	response, err := c.request(ctx, http.MethodDelete, path, "", nil)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (c *Client) imageConfig(ctx context.Context, repository, digest string) (ImageConfig, error) {
	path := "/v2/" + escapeRepository(repository) + "/blobs/" + digest
	response, err := c.request(ctx, http.MethodGet, path, "application/octet-stream", nil)
	if err != nil {
		return ImageConfig{}, err
	}
	defer response.Body.Close()
	var config ImageConfig
	if err := json.NewDecoder(io.LimitReader(response.Body, maxConfigSize)).Decode(&config); err != nil {
		return ImageConfig{}, err
	}
	return config, nil
}

func (c *Client) getPage(ctx context.Context, path string, target any) (bool, error) {
	response, err := c.request(ctx, http.MethodGet, path, "application/json", nil)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(io.LimitReader(response.Body, maxResponseSize)).Decode(target); err != nil {
		return false, fmt.Errorf("decode registry response: %w", err)
	}
	return response.Header.Get("Link") != "", nil
}

func (c *Client) request(ctx context.Context, method, path, contentType string, body io.Reader) (*http.Response, error) {
	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + path
	endpoint.RawQuery = ""
	if queryAt := strings.Index(path, "?"); queryAt >= 0 {
		endpoint.Path = strings.TrimRight(c.baseURL.Path, "/") + path[:queryAt]
		endpoint.RawQuery = path[queryAt+1:]
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		if method == http.MethodPut {
			request.Header.Set("Content-Type", contentType)
		} else {
			request.Header.Set("Accept", contentType)
		}
	}

	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("registry request failed: %w", err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	message := registryErrorMessage(response.Body)
	if message == "" {
		message = response.Status
	}
	return nil, &Error{StatusCode: response.StatusCode, Message: message}
}

func registryErrorMessage(body io.Reader) string {
	var payload struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&payload) == nil && len(payload.Errors) > 0 {
		return payload.Errors[0].Message
	}
	return ""
}

func escapeRepository(repository string) string {
	return repository
}
