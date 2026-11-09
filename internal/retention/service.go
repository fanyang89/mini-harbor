package retention

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/fanyang89/mini-harbor/internal/registry"
)

const (
	planLifetime  = 10 * time.Minute
	maxCandidates = 200
)

var (
	ErrInvalidUnusedForDays = errors.New("unusedForDays must be between 1 and 3650")
	ErrCandidateLimit       = errors.New("cleanup preview exceeds candidate limit")
	ErrPlanNotFound         = errors.New("cleanup plan not found")
	ErrPlanExpired          = errors.New("cleanup plan expired")
	ErrPlanUsed             = errors.New("cleanup plan has already been used")
	ErrRegistry             = errors.New("registry operation failed")
	ErrUsage                = errors.New("image usage storage failed")
)

type Registry interface {
	Repositories(context.Context) ([]string, error)
	Images(context.Context, string) ([]registry.Image, error)
	DeleteManifest(context.Context, string, string) error
}

type Usage interface {
	LastUsed(repository, digest string) (time.Time, bool, error)
}

type PlanStore interface {
	SavePlan(context.Context, string, string, time.Time) error
	ClaimPlan(context.Context, string, time.Time) (string, string, string, bool, error)
	CompletePlan(context.Context, string, string, time.Time) error
}

type Candidate struct {
	Repository  string    `json:"repository"`
	Digest      string    `json:"digest"`
	Tags        []string  `json:"tags"`
	LastUsed    time.Time `json:"lastUsed"`
	LogicalSize int64     `json:"logicalSize"`
}

type Preview struct {
	PlanID        string      `json:"planId"`
	GeneratedAt   time.Time   `json:"generatedAt"`
	ExpiresAt     time.Time   `json:"expiresAt"`
	Cutoff        time.Time   `json:"cutoff"`
	Candidates    []Candidate `json:"candidates"`
	ManifestCount int         `json:"manifestCount"`
	TagCount      int         `json:"tagCount"`
	LogicalSize   int64       `json:"logicalSize"`
}

type ItemResult struct {
	Repository string `json:"repository"`
	Digest     string `json:"digest"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
}

type Execution struct {
	DeletedCount int          `json:"deletedCount"`
	SkippedCount int          `json:"skippedCount"`
	FailedCount  int          `json:"failedCount"`
	Results      []ItemResult `json:"results"`
}

type storedPlan struct {
	Preview
	used      bool
	execution *Execution
}

type Service struct {
	registry Registry
	usage    Usage
	shared   PlanStore

	mu    sync.Mutex
	plans map[string]*storedPlan
	now   func() time.Time
}

func New(registry Registry, usage Usage, shared ...PlanStore) *Service {
	s := &Service{
		registry: registry,
		usage:    usage,
		plans:    make(map[string]*storedPlan),
		now:      time.Now,
	}
	if len(shared) > 0 {
		s.shared = shared[0]
	}
	return s
}

func (s *Service) Preview(ctx context.Context, unusedForDays int) (Preview, error) {
	if unusedForDays < 1 || unusedForDays > 3650 {
		return Preview{}, ErrInvalidUnusedForDays
	}
	if s.registry == nil {
		return Preview{}, fmt.Errorf("%w: registry is unavailable", ErrRegistry)
	}
	if s.usage == nil {
		return Preview{}, fmt.Errorf("%w: image usage store is unavailable", ErrUsage)
	}

	generatedAt := s.now().UTC()
	cutoff := generatedAt.Add(-time.Duration(unusedForDays) * 24 * time.Hour)
	repositories, err := s.registry.Repositories(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("%w: list repositories: %v", ErrRegistry, err)
	}
	sort.Strings(repositories)

	candidates := make([]Candidate, 0)
	for i, repository := range repositories {
		if i > 0 && repository == repositories[i-1] {
			continue
		}
		images, err := s.registry.Images(ctx, repository)
		if err != nil {
			if registryNotFound(err) {
				continue
			}
			return Preview{}, fmt.Errorf("%w: list images for %s: %v", ErrRegistry, repository, err)
		}
		groups := groupImages(images)
		digests := make([]string, 0, len(groups))
		for digest := range groups {
			digests = append(digests, digest)
		}
		sort.Strings(digests)
		for _, digest := range digests {
			lastUsed, ok, err := s.usage.LastUsed(repository, digest)
			if err != nil {
				return Preview{}, fmt.Errorf("%w: read %s@%s: %v", ErrUsage, repository, digest, err)
			}
			if !ok || !lastUsed.Before(cutoff) {
				continue
			}
			group := groups[digest]
			candidates = append(candidates, Candidate{
				Repository:  repository,
				Digest:      digest,
				Tags:        group.tags,
				LastUsed:    lastUsed.UTC(),
				LogicalSize: group.logicalSize,
			})
			if len(candidates) > maxCandidates {
				return Preview{}, fmt.Errorf("%w: maximum is %d", ErrCandidateLimit, maxCandidates)
			}
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Repository != candidates[j].Repository {
			return candidates[i].Repository < candidates[j].Repository
		}
		return candidates[i].Digest < candidates[j].Digest
	})
	planID, err := newPlanID()
	if err != nil {
		return Preview{}, fmt.Errorf("generate cleanup plan ID: %w", err)
	}
	expiresAt := s.now().UTC().Add(planLifetime)
	preview := Preview{
		PlanID:        planID,
		GeneratedAt:   generatedAt,
		ExpiresAt:     expiresAt,
		Cutoff:        cutoff,
		Candidates:    candidates,
		ManifestCount: len(candidates),
	}
	for _, candidate := range candidates {
		preview.TagCount += len(candidate.Tags)
		preview.LogicalSize += candidate.LogicalSize
	}

	if s.shared != nil {
		data, err := json.Marshal(preview)
		if err != nil {
			return Preview{}, err
		}
		if err := s.shared.SavePlan(ctx, planID, string(data), expiresAt); err != nil {
			return Preview{}, err
		}
	} else {
		s.mu.Lock()
		s.removeExpiredLocked(generatedAt)
		s.plans[planID] = &storedPlan{Preview: clonePreview(preview)}
		s.mu.Unlock()
	}
	return preview, nil
}

func (s *Service) Execute(ctx context.Context, planID string) (Execution, error) {
	if s.shared != nil {
		return s.executeShared(ctx, planID)
	}
	now := s.now().UTC()
	s.mu.Lock()
	plan, ok := s.plans[planID]
	if !ok {
		s.mu.Unlock()
		return Execution{}, ErrPlanNotFound
	}
	if !plan.ExpiresAt.After(now) {
		delete(s.plans, planID)
		s.mu.Unlock()
		return Execution{}, ErrPlanExpired
	}
	if plan.used {
		if plan.execution != nil {
			execution := cloneExecution(*plan.execution)
			s.mu.Unlock()
			return execution, nil
		}
		s.mu.Unlock()
		return Execution{}, ErrPlanUsed
	}
	plan.used = true
	candidates := cloneCandidates(plan.Candidates)
	cutoff := plan.Cutoff
	s.mu.Unlock()
	execution := s.executeCandidates(ctx, candidates, cutoff)
	s.mu.Lock()
	if plan, ok := s.plans[planID]; ok {
		stored := cloneExecution(execution)
		plan.execution = &stored
		plan.ExpiresAt = s.now().UTC().Add(planLifetime)
	}
	s.mu.Unlock()
	return execution, nil
}

func (s *Service) executeCandidates(ctx context.Context, candidates []Candidate, cutoff time.Time) Execution {
	execution := Execution{Results: make([]ItemResult, 0, len(candidates))}
	for start := 0; start < len(candidates); {
		end := start + 1
		for end < len(candidates) && candidates[end].Repository == candidates[start].Repository {
			end++
		}
		images, err := s.registry.Images(ctx, candidates[start].Repository)
		if err != nil {
			status := "failed"
			reason := "registry revalidation failed"
			if registryNotFound(err) {
				status = "skipped"
				reason = "digest no longer exists"
			}
			for _, candidate := range candidates[start:end] {
				execution.Results = append(execution.Results, ItemResult{
					Repository: candidate.Repository,
					Digest:     candidate.Digest,
					Status:     status,
					Reason:     reason,
				})
				if status == "skipped" {
					execution.SkippedCount++
				} else {
					execution.FailedCount++
				}
			}
			start = end
			continue
		}
		groups := groupImages(images)
		for _, candidate := range candidates[start:end] {
			result := ItemResult{Repository: candidate.Repository, Digest: candidate.Digest}
			current, exists := groups[candidate.Digest]
			if !exists {
				result.Status = "skipped"
				result.Reason = "digest no longer exists"
				execution.SkippedCount++
				execution.Results = append(execution.Results, result)
				continue
			}
			if !slices.Equal(current.tags, candidate.Tags) {
				result.Status = "skipped"
				result.Reason = "tag set changed"
				execution.SkippedCount++
				execution.Results = append(execution.Results, result)
				continue
			}
			lastUsed, exists, err := s.usage.LastUsed(candidate.Repository, candidate.Digest)
			if err != nil {
				result.Status = "failed"
				result.Reason = "usage revalidation failed"
				execution.FailedCount++
				execution.Results = append(execution.Results, result)
				continue
			}
			if !exists {
				result.Status = "skipped"
				result.Reason = "usage record is missing"
				execution.SkippedCount++
				execution.Results = append(execution.Results, result)
				continue
			}
			if !lastUsed.Before(cutoff) {
				result.Status = "skipped"
				result.Reason = "image usage is no longer stale"
				execution.SkippedCount++
				execution.Results = append(execution.Results, result)
				continue
			}
			if err := s.registry.DeleteManifest(ctx, candidate.Repository, candidate.Digest); err != nil {
				result.Status = "failed"
				result.Reason = "manifest deletion failed"
				execution.FailedCount++
				execution.Results = append(execution.Results, result)
				continue
			}
			result.Status = "deleted"
			execution.DeletedCount++
			execution.Results = append(execution.Results, result)
		}
		start = end
	}
	return execution
}

func (s *Service) executeShared(ctx context.Context, id string) (Execution, error) {
	preview, result, state, claimed, err := s.shared.ClaimPlan(ctx, id, s.now())
	if err != nil {
		return Execution{}, err
	}
	if !claimed {
		switch state {
		case "missing":
			return Execution{}, ErrPlanNotFound
		case "expired":
			return Execution{}, ErrPlanExpired
		case "done":
			var execution Execution
			err := json.Unmarshal([]byte(result), &execution)
			return execution, err
		default:
			return Execution{}, ErrPlanUsed
		}
	}
	var plan Preview
	if err := json.Unmarshal([]byte(preview), &plan); err != nil {
		return Execution{}, err
	}
	execution := s.executeCandidates(ctx, plan.Candidates, plan.Cutoff)
	data, err := json.Marshal(execution)
	if err != nil {
		return Execution{}, err
	}
	// Preserve the outcome even if the caller disconnects. Never automatically
	// release a running claim after an uncertain database response.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.shared.CompletePlan(finish, id, string(data), s.now().Add(planLifetime)); err != nil {
		return Execution{}, err
	}
	return execution, nil
}

type imageGroup struct {
	tags        []string
	logicalSize int64
}

func groupImages(images []registry.Image) map[string]imageGroup {
	groups := make(map[string]imageGroup)
	for _, image := range images {
		if image.Digest == "" {
			continue
		}
		group := groups[image.Digest]
		if image.Tag != "" && !slices.Contains(group.tags, image.Tag) {
			group.tags = append(group.tags, image.Tag)
		}
		if image.Size > group.logicalSize {
			group.logicalSize = image.Size
		}
		groups[image.Digest] = group
	}
	for digest, group := range groups {
		sort.Strings(group.tags)
		groups[digest] = group
	}
	return groups
}

func registryNotFound(err error) bool {
	var registryErr *registry.Error
	return errors.As(err, &registryErr) && registryErr.StatusCode == 404
}

func newPlanID() (string, error) {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func clonePreview(preview Preview) Preview {
	preview.Candidates = cloneCandidates(preview.Candidates)
	return preview
}

func cloneCandidates(candidates []Candidate) []Candidate {
	cloned := make([]Candidate, len(candidates))
	for i, candidate := range candidates {
		cloned[i] = candidate
		cloned[i].Tags = slices.Clone(candidate.Tags)
	}
	return cloned
}

func cloneExecution(execution Execution) Execution {
	execution.Results = slices.Clone(execution.Results)
	return execution
}

func (s *Service) removeExpiredLocked(now time.Time) {
	for id, plan := range s.plans {
		if !plan.ExpiresAt.After(now) && (!plan.used || plan.execution != nil) {
			delete(s.plans, id)
		}
	}
}
