package s3

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	awss3 "github.com/aws/aws-sdk-go/service/s3"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
)

// Bound temporary metadata for large registries and GC walks. The upstream
// directory walker remains the correctness-preserving, constant-space fallback.
const orderedWalkLimit = 100_000

type orderedWalkEntry struct {
	info storagedriver.FileInfoInternal
	key  string
}

func (d *driver) walkOrdered(ctx context.Context, from string, f storagedriver.WalkFn, options ...func(*storagedriver.WalkOptions)) error {
	return d.walkOrderedLimit(ctx, from, f, orderedWalkLimit, options...)
}

func (d *driver) walkOrderedLimit(ctx context.Context, from string, f storagedriver.WalkFn, limit int, options ...func(*storagedriver.WalkOptions)) error {
	settings := &storagedriver.WalkOptions{}
	for _, option := range options {
		option(settings)
	}
	root := strings.Trim(d.RootDirectory, "/")
	if root != "" {
		root += "/"
	}
	entries := make(map[string]storagedriver.FileInfoInternal)
	overflow := false
	err := d.S3.ListObjectsV2PagesWithContext(ctx, &awss3.ListObjectsV2Input{
		Bucket: aws.String(d.Bucket), Prefix: aws.String(d.s3Path(strings.TrimSuffix(from, "/") + "/")),
		MaxKeys: aws.Int64(listMax),
		// Do not send StartAfter: raw S3 key order differs from directory order,
		// and Garage treats a marker outside Prefix as an empty result.
	}, func(page *awss3.ListObjectsV2Output, _ bool) bool {
		for _, object := range page.Contents {
			name := "/" + strings.TrimPrefix(aws.StringValue(object.Key), root)
			directory := strings.HasSuffix(name, "/")
			name = strings.TrimSuffix(name, "/")
			if name == from {
				continue
			}
			entries[name] = storagedriver.FileInfoInternal{FileInfoFields: storagedriver.FileInfoFields{
				Path: name, Size: aws.Int64Value(object.Size), ModTime: aws.TimeValue(object.LastModified), IsDir: directory,
			}}
			for parent := path.Dir(name); parent != from && parent != "/"; parent = path.Dir(parent) {
				if _, exists := entries[parent]; exists {
					break
				}
				entries[parent] = storagedriver.FileInfoInternal{FileInfoFields: storagedriver.FileInfoFields{Path: parent, IsDir: true}}
			}
			if len(entries) > limit {
				overflow = true
				return false
			}
		}
		return true
	})
	if err != nil {
		return err
	}
	if overflow {
		clear(entries)
		return storagedriver.WalkFallback(ctx, d, from, f, options...)
	}

	ordered := make([]orderedWalkEntry, 0, len(entries))
	for name, info := range entries {
		ordered = append(ordered, orderedWalkEntry{info: info, key: directoryOrder(name)})
	}
	slices.SortFunc(ordered, func(a, b orderedWalkEntry) int { return strings.Compare(a.key, b.key) })
	hint := directoryOrder(settings.StartAfterHint)
	skipped := ""
	for _, entry := range ordered {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.key <= hint || (skipped != "" && strings.HasPrefix(entry.info.Path(), skipped)) {
			continue
		}
		err := f(entry.info)
		switch {
		case errors.Is(err, storagedriver.ErrSkipDir):
			skipped = entry.info.Path() + "/"
		case errors.Is(err, storagedriver.ErrFilledBuffer):
			return nil
		case err != nil:
			return err
		}
	}
	return nil
}

func directoryOrder(name string) string {
	return strings.ReplaceAll(name, "/", "\x00")
}
