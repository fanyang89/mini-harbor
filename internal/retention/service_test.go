package retention

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

type fakeRegistry struct {
	mu             sync.Mutex
	repositories   []string
	images         map[string][]registry.Image
	imageErrors    map[string]error
	deleteErrors   map[string]error
	deleted        []string
	imageCalls     map[string]int
	deleteStarted  chan struct{}
	deleteContinue chan struct{}
}

func (f *fakeRegistry) Repositories(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.repositories), nil
}

func (f *fakeRegistry) Images(_ context.Context, repository string) ([]registry.Image, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.imageCalls == nil {
		f.imageCalls = make(map[string]int)
	}
	f.imageCalls[repository]++
	return slices.Clone(f.images[repository]), f.imageErrors[repository]
}

func (f *fakeRegistry) DeleteManifest(_ context.Context, repository, digest string) error {
	if f.deleteStarted != nil {
		close(f.deleteStarted)
		<-f.deleteContinue
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, repository+"@"+digest)
	return f.deleteErrors[repository+"@"+digest]
}

type usageRecord struct {
	lastUsed time.Time
	exists   bool
	err      error
}

type fakeUsage struct {
	mu      sync.Mutex
	records map[string]usageRecord
}

func (f *fakeUsage) LastUsed(repository, digest string) (time.Time, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := f.records[repository+"@"+digest]
	return record.lastUsed, record.exists, record.err
}

func TestPreviewGroupsStaleImagesAndExcludesUnknownUsage(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	client := &fakeRegistry{
		repositories: []string{"tools/runner", "team/app"},
		images: map[string][]registry.Image{
			"team/app": {
				{Tag: "zeta", Digest: "sha256:bbbb", Size: 10},
				{Tag: "alpha", Digest: "sha256:bbbb", Size: 12},
				{Tag: "unknown", Digest: "sha256:cccc", Size: 20},
				{Tag: "boundary", Digest: "sha256:dddd", Size: 30},
			},
			"tools/runner": {{Tag: "old", Digest: "sha256:aaaa", Size: 7}},
		},
	}
	usage := &fakeUsage{records: map[string]usageRecord{
		"team/app@sha256:bbbb":     {lastUsed: now.Add(-49 * time.Hour), exists: true},
		"team/app@sha256:dddd":     {lastUsed: now.Add(-48 * time.Hour), exists: true},
		"tools/runner@sha256:aaaa": {lastUsed: now.Add(-72 * time.Hour), exists: true},
	}}
	service := New(client, usage)
	service.now = func() time.Time { return now }

	preview, err := service.Preview(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if preview.GeneratedAt != now || preview.ExpiresAt != now.Add(10*time.Minute) || preview.Cutoff != now.Add(-48*time.Hour) {
		t.Fatalf("unexpected preview times: %#v", preview)
	}
	if preview.ManifestCount != 2 || preview.TagCount != 3 || preview.LogicalSize != 19 {
		t.Fatalf("unexpected totals: %#v", preview)
	}
	if len(preview.Candidates) != 2 {
		t.Fatalf("unexpected candidates: %#v", preview.Candidates)
	}
	first := preview.Candidates[0]
	if first.Repository != "team/app" || first.Digest != "sha256:bbbb" || !slices.Equal(first.Tags, []string{"alpha", "zeta"}) || first.LogicalSize != 12 {
		t.Fatalf("unexpected grouped candidate: %#v", first)
	}
	if preview.Candidates[1].Repository != "tools/runner" {
		t.Fatalf("candidates are not sorted: %#v", preview.Candidates)
	}
}

func TestPreviewValidatesDaysAndCapsCandidates(t *testing.T) {
	service := New(&fakeRegistry{}, &fakeUsage{})
	for _, days := range []int{0, -1, 3651} {
		if _, err := service.Preview(context.Background(), days); !errors.Is(err, ErrInvalidUnusedForDays) {
			t.Fatalf("days %d: got %v", days, err)
		}
	}

	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	images := make([]registry.Image, maxCandidates+1)
	records := make(map[string]usageRecord, len(images))
	for i := range images {
		digest := fmt.Sprintf("sha256:%04d", i)
		images[i] = registry.Image{Tag: fmt.Sprintf("tag-%d", i), Digest: digest, Size: 1}
		records["team/app@"+digest] = usageRecord{lastUsed: now.Add(-48 * time.Hour), exists: true}
	}
	client := &fakeRegistry{repositories: []string{"team/app"}, images: map[string][]registry.Image{"team/app": images}}
	service = New(client, &fakeUsage{records: records})
	service.now = func() time.Time { return now }
	if _, err := service.Preview(context.Background(), 1); !errors.Is(err, ErrCandidateLimit) {
		t.Fatalf("expected candidate limit, got %v", err)
	}
}

func TestPreviewSkipsStaleCatalogEntries(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	client := &fakeRegistry{
		repositories: []string{"deleted/repository", "team/app"},
		images: map[string][]registry.Image{
			"team/app": {{Tag: "old", Digest: "sha256:a", Size: 1}},
		},
		imageErrors: map[string]error{
			"deleted/repository": &registry.Error{StatusCode: 404, Message: "repository not found"},
		},
	}
	usage := &fakeUsage{records: map[string]usageRecord{
		"team/app@sha256:a": {lastUsed: now.Add(-48 * time.Hour), exists: true},
	}}
	service := New(client, usage)
	service.now = func() time.Time { return now }

	preview, err := service.Preview(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ManifestCount != 1 || preview.Candidates[0].Repository != "team/app" {
		t.Fatalf("unexpected candidates: %#v", preview.Candidates)
	}
}

func TestPreviewReturnsOtherRepositoryErrors(t *testing.T) {
	client := &fakeRegistry{
		repositories: []string{"team/app"},
		images:       map[string][]registry.Image{},
		imageErrors:  map[string]error{"team/app": errors.New("unavailable")},
	}
	service := New(client, &fakeUsage{records: map[string]usageRecord{}})

	if _, err := service.Preview(context.Background(), 1); !errors.Is(err, ErrRegistry) {
		t.Fatalf("expected registry error, got %v", err)
	}
}

func TestExecuteRevalidatesCandidatesAndContinuesAfterDeleteFailure(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)
	images := []registry.Image{
		{Tag: "old", Digest: "sha256:a", Size: 1},
		{Tag: "old", Digest: "sha256:b", Size: 1},
		{Tag: "old", Digest: "sha256:c", Size: 1},
		{Tag: "old", Digest: "sha256:d", Size: 1},
		{Tag: "old", Digest: "sha256:e", Size: 1},
		{Tag: "old", Digest: "sha256:f", Size: 1},
	}
	records := make(map[string]usageRecord, len(images))
	for _, image := range images {
		records["team/app@"+image.Digest] = usageRecord{lastUsed: stale, exists: true}
	}
	client := &fakeRegistry{
		repositories: []string{"team/app"},
		images:       map[string][]registry.Image{"team/app": images},
		deleteErrors: map[string]error{"team/app@sha256:a": errors.New("registry unavailable")},
	}
	usage := &fakeUsage{records: records}
	service := New(client, usage)
	service.now = func() time.Time { return now }
	preview, err := service.Preview(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	client.mu.Lock()
	client.images["team/app"] = []registry.Image{
		{Tag: "old", Digest: "sha256:a", Size: 1},
		{Tag: "old", Digest: "sha256:b", Size: 1},
		{Tag: "old", Digest: "sha256:c", Size: 1},
		{Tag: "new", Digest: "sha256:c", Size: 1},
		{Tag: "old", Digest: "sha256:e", Size: 1},
		{Tag: "old", Digest: "sha256:f", Size: 1},
	}
	client.mu.Unlock()
	usage.mu.Lock()
	usage.records["team/app@sha256:e"] = usageRecord{lastUsed: now, exists: true}
	delete(usage.records, "team/app@sha256:f")
	usage.mu.Unlock()

	execution, err := service.Execute(context.Background(), preview.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.DeletedCount != 1 || execution.SkippedCount != 4 || execution.FailedCount != 1 {
		t.Fatalf("unexpected execution counts: %#v", execution)
	}
	if len(execution.Results) != 6 || execution.Results[0].Status != "failed" || execution.Results[1].Status != "deleted" {
		t.Fatalf("unexpected execution results: %#v", execution.Results)
	}
	if execution.Results[2].Reason != "tag set changed" || execution.Results[3].Reason != "digest no longer exists" || execution.Results[4].Reason != "image usage is no longer stale" || execution.Results[5].Reason != "usage record is missing" {
		t.Fatalf("unexpected skip reasons: %#v", execution.Results)
	}
	if !slices.Equal(client.deleted, []string{"team/app@sha256:a", "team/app@sha256:b"}) {
		t.Fatalf("unexpected delete calls: %#v", client.deleted)
	}
	if client.imageCalls["team/app"] != 2 {
		t.Fatalf("expected one preview and one execution scan, got %d", client.imageCalls["team/app"])
	}
}

func TestExecuteConsumesPlanBeforeDeletion(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	resume := make(chan struct{})
	client := &fakeRegistry{
		repositories:   []string{"team/app"},
		images:         map[string][]registry.Image{"team/app": {{Tag: "old", Digest: "sha256:a", Size: 1}}},
		deleteErrors:   map[string]error{},
		deleteStarted:  started,
		deleteContinue: resume,
	}
	usage := &fakeUsage{records: map[string]usageRecord{
		"team/app@sha256:a": {lastUsed: now.Add(-48 * time.Hour), exists: true},
	}}
	service := New(client, usage)
	service.now = func() time.Time { return now }
	preview, err := service.Preview(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	finished := make(chan error, 1)
	go func() {
		_, err := service.Execute(context.Background(), preview.PlanID)
		finished <- err
	}()
	<-started
	if _, err := service.Execute(context.Background(), preview.PlanID); !errors.Is(err, ErrPlanUsed) {
		t.Fatalf("second execute returned %v", err)
	}
	close(resume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestExecuteRejectsExpiredPlan(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	client := &fakeRegistry{repositories: []string{}, images: map[string][]registry.Image{}}
	service := New(client, &fakeUsage{records: map[string]usageRecord{}})
	service.now = func() time.Time { return now }
	preview, err := service.Preview(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now.Add(10 * time.Minute) }
	if _, err := service.Execute(context.Background(), preview.PlanID); !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("expected expired plan, got %v", err)
	}
}

func TestExecuteReturnsCompletedResultWithoutDeletingAgain(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	client := &fakeRegistry{
		repositories: []string{"team/app"},
		images: map[string][]registry.Image{
			"team/app": {{Tag: "old", Digest: "sha256:a", Size: 1}},
		},
	}
	usage := &fakeUsage{records: map[string]usageRecord{
		"team/app@sha256:a": {lastUsed: now.Add(-48 * time.Hour), exists: true},
	}}
	service := New(client, usage)
	service.now = func() time.Time { return now }
	preview, err := service.Preview(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Execute(context.Background(), preview.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Execute(context.Background(), preview.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeletedCount != 1 || second.DeletedCount != 1 || len(client.deleted) != 1 {
		t.Fatalf("unexpected replay: first=%#v second=%#v deletes=%#v", first, second, client.deleted)
	}
}

func TestExecuteTreatsMissingRepositoryAsSkipped(t *testing.T) {
	now := time.Date(2026, time.August, 23, 12, 0, 0, 0, time.UTC)
	client := &fakeRegistry{
		repositories: []string{"team/app"},
		images: map[string][]registry.Image{
			"team/app": {{Tag: "old", Digest: "sha256:a", Size: 1}},
		},
	}
	usage := &fakeUsage{records: map[string]usageRecord{
		"team/app@sha256:a": {lastUsed: now.Add(-48 * time.Hour), exists: true},
	}}
	service := New(client, usage)
	service.now = func() time.Time { return now }
	preview, err := service.Preview(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	client.mu.Lock()
	client.imageErrors = map[string]error{
		"team/app": &registry.Error{StatusCode: 404, Message: "repository not found"},
	}
	client.mu.Unlock()
	execution, err := service.Execute(context.Background(), preview.PlanID)
	if err != nil {
		t.Fatal(err)
	}
	if execution.SkippedCount != 1 || execution.Results[0].Reason != "digest no longer exists" {
		t.Fatalf("unexpected execution: %#v", execution)
	}
}
