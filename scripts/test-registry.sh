#!/bin/sh
# Test the S3 overlay against the exact Distribution source used by Dockerfile.
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
base=${TMPDIR:-$HOME/tmp}
mkdir -p "$base"
work=$(mktemp -d "$base/mini-harbor-registry.XXXXXXXX")
trap 'rm -rf "$work"' EXIT
trap 'exit 1' HUP INT TERM
archive=$work/distribution.tar.gz
if [ -n "${DISTRIBUTION_ARCHIVE:-}" ]; then
    cp "$DISTRIBUTION_ARCHIVE" "$archive"
else
    curl --fail --location --max-time 120 \
        https://codeload.github.com/distribution/distribution/tar.gz/9a8d98b679740cd514aa7e7d84d23d442a5ef54c \
        --output "$archive"
fi
printf '%s  %s\n' db8d38f341b7699e516eaa33986814cce3276e75d1df4ac6f77d60e58c688c45 "$archive" | sha256sum -c -
mkdir "$work/source"
tar -xzf "$archive" --strip-components=1 -C "$work/source"
patch --fuzz=0 -d "$work/source" -p1 < "$root/registry/_distribution/ordered-walk.patch"
cp "$root"/registry/_distribution/*.go "$work/source/registry/storage/driver/s3-aws/"
cd "$work/source"
go test -race ./registry/storage/driver/s3-aws -run '^TestOrderedWalk' -count=1
