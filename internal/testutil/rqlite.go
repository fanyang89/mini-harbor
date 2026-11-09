// Package testutil provides opt-in integration fixtures backed by real rqlite.
package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const RqliteImage = "rqlite/rqlite:10.6.0@sha256:a63af1b16b051ed7d8c81b57b4d704d47faaf1c0002f60c8b6bb06a41535644a"

type Cluster struct {
	Nodes  []string
	URLs   []string
	client *http.Client
}

func Docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func StartRqlite(t *testing.T, count int) *Cluster {
	t.Helper()
	if os.Getenv("RQLITE_TESTS") != "1" {
		t.Skip("set RQLITE_TESTS=1 to run real rqlite integration tests (Docker required)")
	}
	name := fmt.Sprintf("mini-harbor-go-test-%d", time.Now().UnixNano())
	c := &Cluster{client: &http.Client{Timeout: time.Second}}
	Docker(t, "network", "create", name)
	t.Cleanup(func() {
		for _, node := range c.Nodes {
			if t.Failed() {
				t.Log(Docker(t, "logs", "--tail", "30", node))
			}
			Docker(t, "rm", "-f", "-v", node)
		}
		Docker(t, "network", "rm", name)
		c.client.CloseIdleConnections()
	})
	var joins []string
	for i := range count {
		joins = append(joins, fmt.Sprintf("%s-n%d:4002", name, i))
	}
	for i := range count {
		node := fmt.Sprintf("%s-n%d", name, i)
		args := []string{"run", "-d", "--name", node, "--hostname", node, "--network", name, "-p", "127.0.0.1::4001", RqliteImage}
		if count > 1 {
			args = append(args, "-bootstrap-expect", fmt.Sprint(count), "-join", strings.Join(joins, ","))
		}
		Docker(t, args...)
		c.Nodes = append(c.Nodes, node)
		c.URLs = append(c.URLs, "http://"+Docker(t, "port", node, "4001/tcp"))
	}
	Wait(t, "cluster readiness", func() bool {
		for _, endpoint := range c.URLs {
			resp, err := c.client.Get(endpoint + "/readyz")
			if err != nil {
				return false
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return false
			}
		}
		return true
	})
	return c
}

func Wait(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func (c *Cluster) Leader() int {
	for i, endpoint := range c.URLs {
		resp, err := c.client.Get(endpoint + "/status")
		if err != nil {
			continue
		}
		var status struct {
			Store struct {
				Raft struct {
					State string `json:"state"`
				} `json:"raft"`
			} `json:"store"`
		}
		err = json.NewDecoder(resp.Body).Decode(&status)
		_ = resp.Body.Close()
		if err == nil && status.Store.Raft.State == "Leader" {
			return i
		}
	}
	return -1
}

func (c *Cluster) Restart(t *testing.T, indices ...int) {
	t.Helper()
	for _, i := range indices {
		Docker(t, "start", c.Nodes[i])
		c.URLs[i] = "http://" + Docker(t, "port", c.Nodes[i], "4001/tcp")
	}
}
