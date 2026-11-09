package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/testutil"
)

func TestSIGTERMDrainsActiveRequest(t *testing.T) {
	cluster := testutil.StartRqlite(t, 1)
	binary := filepath.Join(t.TempDir(), "server")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"repositories":[]}`)
	}))
	defer registry.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	cmd := exec.CommandContext(t.Context(), binary)
	cmd.Env = append(os.Environ(), "LISTEN_ADDR="+address, "RQLITE_URLS="+strings.Join(cluster.URLs, ","), "REGISTRY_URL="+registry.URL, "OIDC_ISSUER=")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	client := &http.Client{Timeout: 5 * time.Second}
	testutil.Wait(t, "server ready", func() bool {
		r, err := client.Get("http://" + address + "/api/auth/me")
		if err != nil {
			return false
		}
		r.Body.Close()
		return r.StatusCode == http.StatusOK
	})
	done := make(chan error, 1)
	go func() {
		r, err := client.Get("http://" + address + "/api/repositories")
		if err == nil {
			_, err = io.ReadAll(r.Body)
			r.Body.Close()
		}
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("request did not reach registry")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		close(release)
		t.Fatal(err)
	}
	// Give the process time to receive SIGTERM while the handler is still blocked.
	select {
	case err := <-done:
		close(release)
		t.Fatalf("request ended before release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("active request interrupted: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}
