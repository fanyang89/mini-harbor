package registry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRepositoriesFollowsPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/_catalog" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("last") == "" {
			w.Header().Set("Link", `</v2/_catalog?n=1000&last=team/app>; rel="next"`)
			_, _ = io.WriteString(w, `{"repositories":["team/app"]}`)
			return
		}
		if got := r.URL.Query().Get("last"); got != "team/app" {
			t.Fatalf("unexpected last value: %q", got)
		}
		_, _ = io.WriteString(w, `{"repositories":["tools/runner"]}`)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	repositories, err := client.Repositories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(repositories) != 2 || repositories[0] != "team/app" || repositories[1] != "tools/runner" {
		t.Fatalf("unexpected repositories: %#v", repositories)
	}
}

func TestPutTagCopiesManifest(t *testing.T) {
	const manifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"size":0,"digest":""},"layers":[{"size":42,"digest":"sha256:aa"}]}`
	var putCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/team/app/manifests/latest":
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Docker-Content-Digest", "sha256:source")
			_, _ = io.WriteString(w, manifest)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/team/app/manifests/stable":
			putCalled = true
			if got := r.Header.Get("Content-Type"); got != "application/vnd.oci.image.manifest.v1+json" {
				t.Fatalf("unexpected content type: %q", got)
			}
			body, _ := io.ReadAll(r.Body)
			if string(body) != manifest {
				t.Fatalf("manifest changed: %s", body)
			}
			w.Header().Set("Docker-Content-Digest", "sha256:target")
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	image, err := client.PutTag(context.Background(), "team/app", "latest", "stable")
	if err != nil {
		t.Fatal(err)
	}
	if !putCalled || image.Tag != "stable" || image.Digest != "sha256:target" || image.Size != 42 {
		t.Fatalf("unexpected result: %#v", image)
	}
}

func TestDeleteTagUsesTagReference(t *testing.T) {
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		requests <- r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteTag(context.Background(), "team/app", "latest"); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteManifest(context.Background(), "team/app", "sha256:abcd"); err != nil {
		t.Fatal(err)
	}
	if got := <-requests; got != "/v2/team/app/manifests/latest" {
		t.Fatalf("unexpected tag path: %s", got)
	}
	if got := <-requests; got != "/v2/team/app/manifests/sha256:abcd" {
		t.Fatalf("unexpected digest path: %s", got)
	}
}
