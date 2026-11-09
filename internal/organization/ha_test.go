package organization

import (
	"errors"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/testutil"
)

func TestStoreFailover(t *testing.T) {
	cluster := testutil.StartRqlite(t, 3)
	leader := cluster.Leader()
	if leader < 0 {
		t.Fatal("no leader")
	}
	// Start with the leader's endpoint so the same Store must discover another
	// endpoint after the crash, rather than relying on test-side leader routing.
	endpoints := []string{cluster.URLs[leader]}
	for i, endpoint := range cluster.URLs {
		if i != leader {
			endpoints = append(endpoints, endpoint)
		}
	}
	store, err := Open(t.Context(), endpoints...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	created, err := store.Create(t.Context(), "platform", "研发 ' Platform", "HA")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	testutil.Docker(t, "kill", "--signal=KILL", cluster.Nodes[leader])
	testutil.Wait(t, "Store reads through a surviving endpoint", func() bool {
		got, err := store.Get(t.Context(), "platform")
		return err == nil && got == created
	})
	if _, err := store.Create(t.Context(), "after-failover", "HA", ""); err != nil {
		t.Fatal(err)
	}
	t.Logf("Go Store crash-to-successful-write: %s", time.Since(started))
	second := cluster.Leader()
	if second < 0 || second == leader {
		t.Fatal("replacement leader not found")
	}
	testutil.Docker(t, "kill", "--signal=KILL", cluster.Nodes[second])
	if _, err := store.Create(t.Context(), "minority", "Unavailable", ""); err == nil {
		t.Fatal("minority acknowledged write")
	}
	cluster.Restart(t, leader, second)
	testutil.Wait(t, "recovered cluster", func() bool { return cluster.Leader() >= 0 })
	// Docker reallocates published ports on restart; a new process gets the
	// new test URLs. Production endpoints must have stable addresses.
	recovered, err := Open(t.Context(), cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
	items, err := recovered.List(t.Context())
	if err != nil || len(items) != 2 {
		t.Fatalf("recovered rows: %v, %v", items, err)
	}
	if _, err := recovered.Get(t.Context(), "minority"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("minority row: %v", err)
	}
	// Inspect local replicas, not reads forwarded to the leader.
	testutil.Wait(t, "all replicas apply committed rows", func() bool {
		for i := range recovered.endpoints {
			results, err := recovered.request(t.Context(), i, "/db/query?level=none", [][]any{{"SELECT " + columns + " FROM organizations ORDER BY id"}})
			if err != nil || len(results[0].Values) != 2 {
				return false
			}
			for j, row := range results[0].Values {
				item, err := decodeOrganization(row)
				if err != nil || item != items[j] {
					return false
				}
			}
		}
		return true
	})
}
