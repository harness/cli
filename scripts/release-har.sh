#!/bin/sh
# Builds and publishes a release for the har plugin, independent of core's
# own goreleaser-driven release. Triggered off tags matching har/vX.Y.Z.
#
# Usage:
#   scripts/release-har.sh <tag> [--publish]
#
# <tag> must be exactly har/vX.Y.Z. Artifacts are always built into dist-plugins/har/ so they
# can be inspected — kept out of goreleaser's own dist/ so the two builds
# never mix. Publishing is opt-in: without --publish the script stops after
# building, so a bare/mistyped invocation can never create a GitHub release.
# --publish creates the release as a draft — install/resolve code already
# ignores drafts — so a human still reviews and publishes it from GitHub.
#
# --publish also requires HEAD to actually be the given tag, with no local
# changes on top of it — so the released binaries always match the tagged
# commit, whether run from CI or a workstation.
set -eu

REPO="harness/cli"
PLATFORMS="linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64 windows_arm64"
DIST_DIR="dist-plugins/har"

info()  { printf '  \033[34m•\033[0m %s\n' "$*"; }
error() { printf '  \033[31m✗\033[0m %s\n' "$*" >&2; exit 1; }

TAG="${1:-}"
PUBLISH=""
case "${2:-}" in
    --publish) PUBLISH=1 ;;
    "") ;;
    *) error "unrecognized argument ${2:-} (expected --publish)" ;;
esac

[ -n "$TAG" ] || error "usage: $0 <tag> [--publish]  (e.g. $0 har/v3.1.0 --publish)"

echo "$TAG" | grep -Eq '^har/v[0-9]+\.[0-9]+\.[0-9]+$' || error "tag $TAG does not match har/vX.Y.Z"

if [ -n "$PUBLISH" ]; then
    tag_commit="$(git rev-list -n 1 "refs/tags/$TAG" 2>/dev/null)" || error "tag $TAG not found in this repo"
    head_commit="$(git rev-parse HEAD)"
    [ "$tag_commit" = "$head_commit" ] || error "HEAD ($head_commit) is not tag $TAG ($tag_commit) — checkout the tag before publishing"
    [ -z "$(git status --porcelain)" ] || error "working tree has local changes — publishing must build from a clean checkout of $TAG"
fi

VERSION="${TAG#har/}"      # v3.1.0
VER="${VERSION#v}"         # 3.1.0
BUILD_TIME="$(date -u +%Y%m%d%H%MZ)"
LDFLAGS="-s -w -X github.com/harness/cli/pkg/hbase.Version=${VER} -X github.com/harness/cli/pkg/hbase.BuildTime=${BUILD_TIME}"

rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

info "building harness-har ${VERSION}"
for platform in $PLATFORMS; do
    os="${platform%_*}"
    arch="${platform#*_}"
    binary="harness-har"
    [ "$os" = "windows" ] && binary="harness-har.exe"

    stage="$(mktemp -d)"
    info "  ${platform}"
    (
        cd modules/har
        GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
            go build -trimpath -ldflags "$LDFLAGS" -o "${stage}/${binary}" ./cmd/harness-har/main-harness-har.go
    )

    archive_base="harness-plugin-har_${VER}_${platform}"
    if [ "$os" = "windows" ]; then
        (cd "$stage" && zip -q "${archive_base}.zip" "$binary")
        mv "${stage}/${archive_base}.zip" "$DIST_DIR/"
    else
        tar -czf "$DIST_DIR/${archive_base}.tar.gz" -C "$stage" "$binary"
    fi
    rm -rf "$stage"
done

info "writing checksums.txt"
(
    cd "$DIST_DIR"
    shasum -a 256 *.tar.gz *.zip > checksums.txt
)

info "artifacts in ${DIST_DIR}:"
ls -la "$DIST_DIR"

if [ -z "$PUBLISH" ]; then
    info "built only — pass --publish to create the GitHub release"
    exit 0
fi

info "creating draft release ${TAG}"
gh release create "$TAG" \
    --repo "$REPO" \
    --title "harness-har ${VERSION}" \
    --notes "harness-har ${VERSION}" \
    --latest=false \
    --draft \
    "$DIST_DIR"/*
