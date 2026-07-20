#!/usr/bin/env bash
# Copy grafana/grafana:10.4.0 (linux/amd64 + linux/arm64) from Docker Hub to
# quay.io/vmware-ai/grafana:10.4.0 — only the two arches this stack supports.
#
# Run this from any host that has Docker Hub egress + push credentials for
# the vmware-ai quay.io org (the same creds used by `make release-images`).
#
# Usage:
#   ./sync_grafana_image.sh                 # default: copy tag 10.4.0
#   SRC_TAG=10.5.0 DST_TAG=10.5.0 \
#     ./sync_grafana_image.sh               # upgrade to a new version
#   PLATFORMS=linux/amd64 ./sync_grafana_image.sh   # single-arch
#
# Idempotent. Re-running overwrites the destination tag. Always carries a
# multi-arch manifest at the destination (even for a single platform), so
# later upgrades to multi-arch don't have to change the consumer side.
#
# Verify after push:
#   docker buildx imagetools inspect quay.io/vmware-ai/grafana:10.4.0
set -euo pipefail

SRC_REPO="docker.io/grafana/grafana"
DST_REPO="quay.io/vmware-ai/grafana"

SRC_TAG="${SRC_TAG:-10.4.0}"
DST_TAG="${DST_TAG:-${SRC_TAG}}"
PLATFORMS="${PLATFORMS:-linux/amd64,linux/arm64}"

# Use the project's multi-arch builder if present (matches `make release-images`),
# otherwise the default docker builder. The imagetools command only needs a
# builder capable of handling the requested platforms — no source build.
BUILDER="${BUILDER:-agent-platform-builder}"
if docker buildx inspect "${BUILDER}" >/dev/null 2>&1; then
  docker buildx use "${BUILDER}"
else
  echo "note: builder '${BUILDER}' not found; using default docker context" >&2
fi

# Make sure quay.io auth is loaded (creds live in ~/.docker/config.json under
# the "quay.io" key — same setup as the rest of the project's release flow).
if ! docker buildx imagetools inspect "${DST_REPO}:${DST_TAG}" >/dev/null 2>&1; then
  echo "note: cannot read existing ${DST_REPO}:${DST_TAG} — verify 'docker login quay.io' is configured" >&2
fi

echo "==> copying ${SRC_REPO}:${SRC_TAG} (${PLATFORMS})"
echo "         to ${DST_REPO}:${DST_TAG}"
docker buildx imagetools create \
  --platform "${PLATFORMS}" \
  --tag "${DST_REPO}:${DST_TAG}" \
  "${SRC_REPO}:${SRC_TAG}"

echo
echo "==> pushed. Manifest now:"
docker buildx imagetools inspect "${DST_REPO}:${DST_TAG}"
