package organization

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
	"github.com/fanyang89/mini-harbor/internal/retention"
	"github.com/fanyang89/mini-harbor/internal/testutil"
	"github.com/fanyang89/mini-harbor/internal/usage"
)

type sharedRegistry struct {
	deletes atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (r *sharedRegistry) Repositories(context.Context) ([]string, error) {
	return []string{"library/test"}, nil
}
func (r *sharedRegistry) Images(context.Context, string) ([]registry.Image, error) {
	return []registry.Image{{Digest: "sha256:test", Tag: "latest"}}, nil
}
func (r *sharedRegistry) DeleteManifest(ctx context.Context, _, _ string) error {
	r.deletes.Add(1)
	close(r.entered)
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSharedUsageAndCleanupAcrossReplicas(t *testing.T) {
	cluster := testutil.StartRqlite(t, 1)
	ctx := t.Context()
	a, err := Open(ctx, cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(ctx, cluster.URLs...)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	latest := time.Now().UTC().Add(-90 * 24 * time.Hour)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			if err := a.Record("library/test", "sha256:test", latest.Add(-time.Duration(i)*time.Second)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, ok, err := b.LastUsed("library/test", "sha256:test")
	if err != nil || !ok || !got.Equal(latest) {
		t.Fatalf("usage: %v %v %v", got, ok, err)
	}
	legacy := []usage.Record{{Repository: "library/test", Digest: "sha256:test", LastUsed: latest.Add(-time.Hour)}}
	if err := ImportUsage(ctx, legacy, cluster.URLs, false); err != nil {
		t.Fatal(err)
	}
	got, _, _ = b.LastUsed("library/test", "sha256:test")
	if !got.Equal(latest) {
		t.Fatal("legacy import overwrote newer usage")
	}
	r := &sharedRegistry{entered: make(chan struct{}), release: make(chan struct{})}
	x, y := retention.New(r, a, a), retention.New(r, b, b)
	plan, err := x.Preview(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	other, err := x.Preview(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := y.Execute(ctx, plan.PlanID); done <- err }()
	select {
	case <-r.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("execution did not start")
	}
	for _, id := range []string{plan.PlanID, other.PlanID} {
		if _, err := x.Execute(ctx, id); !errors.Is(err, retention.ErrPlanUsed) {
			t.Fatalf("concurrent execution: %v", err)
		}
	}
	close(r.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	replayed, err := x.Execute(ctx, plan.PlanID)
	if err != nil || replayed.DeletedCount != 1 || r.deletes.Load() != 1 {
		t.Fatalf("replay: %+v %v deletes=%d", replayed, err, r.deletes.Load())
	}
	// A crashed execution must retain the global lock beyond plan expiry.
	_, _, _, claimed, err := a.ClaimPlan(ctx, other.PlanID, time.Now())
	if err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	if err := b.SavePlan(ctx, "future", "{}", time.Now().Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_, _, _, claimed, err = b.ClaimPlan(ctx, "future", time.Now().Add(24*time.Hour))
	if err != nil || claimed {
		t.Fatalf("abandoned claim was released: %v %v", claimed, err)
	}
}
