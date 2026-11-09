package httpapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/fanyang89/mini-harbor/internal/organization"
	"github.com/fanyang89/mini-harbor/internal/retention"
)

type graphOrganizationInput struct{ Name, DisplayName, Description string }

func (r *graphResolver) saveOrganization(ctx context.Context, input graphOrganizationInput, create bool) (*graphOrganization, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.Description = strings.TrimSpace(input.Description)
	if create && input.DisplayName == "" {
		input.DisplayName = input.Name
	}
	if !organization.ValidName(input.Name) || !validOrganizationDetails(input.DisplayName, input.Description) {
		return nil, invalidGraph("Invalid organization details")
	}
	repositories, err := r.server.registry.Repositories(ctx)
	if err != nil {
		return nil, graphRegistryError(err)
	}
	var item organization.Organization
	if create {
		item, err = r.server.organizations.Create(ctx, input.Name, input.DisplayName, input.Description)
	} else {
		item, err = r.server.organizations.Update(ctx, input.Name, input.DisplayName, input.Description)
	}
	if err != nil {
		return nil, graphOrganizationError(err)
	}
	return organizationGraph(item, repositories), nil
}
func (r *graphResolver) CreateOrganization(ctx context.Context, args struct{ Input graphOrganizationInput }) (*graphOrganization, error) {
	return r.saveOrganization(ctx, args.Input, true)
}
func (r *graphResolver) UpdateOrganization(ctx context.Context, args struct{ Input graphOrganizationInput }) (*graphOrganization, error) {
	return r.saveOrganization(ctx, args.Input, false)
}
func (r *graphResolver) DeleteOrganization(ctx context.Context, args struct{ Name string }) (bool, error) {
	if !organization.ValidName(args.Name) {
		return false, invalidGraph("Invalid organization name")
	}
	// Mutations execute serially; reads here must see preceding mutations.
	repositories, err := r.server.registry.Repositories(ctx)
	if err != nil {
		return false, graphRegistryError(err)
	}
	if repositoryCount(args.Name, repositories) > 0 {
		return false, graphError{"Organization contains repositories", 409}
	}
	err = r.server.organizations.Delete(ctx, args.Name)
	return err == nil, graphOrganizationError(err)
}
func (r *graphResolver) PutTag(ctx context.Context, args struct {
	Repository, Source, Target string
	Overwrite                  bool
}) (*graphImage, error) {
	defer r.server.invalidateCatalog(ctx)
	if !validRepository(args.Repository) || !tagPattern.MatchString(args.Source) || !tagPattern.MatchString(args.Target) {
		return nil, invalidGraph("Invalid repository or tag")
	}
	// Preserve the existing Registry PutTag behavior for both copy and overwrite.
	image, err := r.server.registry.PutTag(ctx, args.Repository, args.Source, args.Target)
	if err != nil {
		return nil, graphRegistryError(err)
	}
	return imageGraph(args.Repository, image), nil
}
func (r *graphResolver) DeleteTag(ctx context.Context, args struct{ Repository, Tag string }) (bool, error) {
	defer r.server.invalidateCatalog(ctx)
	if !validRepository(args.Repository) || !tagPattern.MatchString(args.Tag) {
		return false, invalidGraph("Invalid repository or tag")
	}
	err := r.server.registry.DeleteTag(ctx, args.Repository, args.Tag)
	return err == nil, graphRegistryError(err)
}
func (r *graphResolver) DeleteManifest(ctx context.Context, args struct{ Repository, Digest string }) (bool, error) {
	defer r.server.invalidateCatalog(ctx)
	if !validRepository(args.Repository) || !digestPattern.MatchString(args.Digest) {
		return false, invalidGraph("Manifest deletion requires a digest")
	}
	err := r.server.registry.DeleteManifest(ctx, args.Repository, args.Digest)
	return err == nil, graphRegistryError(err)
}

type graphGCCandidate struct {
	Repository, Digest string
	Tags               []string
	LastUsed           string
	LogicalSize        graphLong
}
type graphGCPreview struct {
	PlanID, GeneratedAt, ExpiresAt, Cutoff string
	Candidates                             []graphGCCandidate
	ManifestCount, TagCount                int32
	LogicalSize                            graphLong
}
type graphGCExecution struct {
	DeletedCount, SkippedCount, FailedCount int32
	Results                                 []retention.ItemResult
}

func graphRetentionError(err error) error {
	if err == nil {
		return nil
	}
	status := 500
	switch {
	case errors.Is(err, retention.ErrInvalidUnusedForDays):
		status = 400
	case errors.Is(err, retention.ErrCandidateLimit):
		status = 422
	case errors.Is(err, retention.ErrPlanNotFound):
		status = 404
	case errors.Is(err, retention.ErrPlanExpired):
		status = 410
	case errors.Is(err, retention.ErrPlanUsed):
		status = 409
	case errors.Is(err, retention.ErrRegistry):
		status = 502
	case errors.Is(err, retention.ErrUsage):
		return graphError{"Image usage storage failed", 500}
	default:
		return graphError{"Image cleanup failed", 500}
	}
	return graphError{err.Error(), status}
}
func (r *graphResolver) PreviewImageGC(ctx context.Context, args struct{ UnusedForDays int32 }) (*graphGCPreview, error) {
	preview, err := r.server.retention.Preview(ctx, int(args.UnusedForDays))
	if err != nil {
		return nil, graphRetentionError(err)
	}
	result := &graphGCPreview{PlanID: preview.PlanID, GeneratedAt: preview.GeneratedAt.Format(time.RFC3339Nano), ExpiresAt: preview.ExpiresAt.Format(time.RFC3339Nano), Cutoff: preview.Cutoff.Format(time.RFC3339Nano), Candidates: []graphGCCandidate{}, ManifestCount: int32(preview.ManifestCount), TagCount: int32(preview.TagCount), LogicalSize: graphLong(preview.LogicalSize)}
	for _, item := range preview.Candidates {
		result.Candidates = append(result.Candidates, graphGCCandidate{item.Repository, item.Digest, item.Tags, item.LastUsed.Format(time.RFC3339Nano), graphLong(item.LogicalSize)})
	}
	return result, nil
}
func (r *graphResolver) ExecuteImageGC(ctx context.Context, args struct{ PlanID, Confirmation string }) (*graphGCExecution, error) {
	defer r.server.invalidateCatalog(ctx)
	if args.PlanID == "" || args.Confirmation != "DELETE" {
		return nil, invalidGraph("Plan ID and DELETE confirmation are required")
	}
	result, err := r.server.retention.Execute(ctx, args.PlanID)
	if err != nil {
		return nil, graphRetentionError(err)
	}
	return &graphGCExecution{int32(result.DeletedCount), int32(result.SkippedCount), int32(result.FailedCount), result.Results}, nil
}
