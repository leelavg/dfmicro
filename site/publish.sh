#!/usr/bin/env bash
set -euo pipefail

repo=$(git rev-parse --show-toplevel)
cd "$repo"

tags=$(git tag --list 'v[0-9]*' --sort=version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true)
if [[ -z "$tags" ]]; then
    printf 'No stable version tags found.\n' >&2
    exit 1
fi
latest=$(printf '%s\n' "$tags" | tail -n 1)
tag_count=$(printf '%s\n' "$tags" | wc -l | tr -d ' ')

build_dir=$(mktemp -d "$repo/site/.build.XXXXXX")
trap 'rm -rf "$build_dir"' EXIT
public="$build_dir/public"
versions="$build_dir/versions.tsv"
branches="$build_dir/branches.tsv"
mkdir -p "$public"
: > "$branches"

{
    printf 'latest|Latest (%s)|/\n' "$latest"
    current_branch=$(git branch --show-current)
    for branch in main forge; do
        if [[ "$current_branch" == "$branch" ]]; then
            ref=working-tree
        elif git rev-parse --verify --quiet "origin/$branch^{commit}" >/dev/null; then
            ref="origin/$branch"
        elif git rev-parse --verify --quiet "$branch^{commit}" >/dev/null; then
            ref=$branch
        else
            printf 'Skipping unavailable branch: %s\n' "$branch" >&2
            continue
        fi

        printf '%s|%s\n' "$branch" "$ref" >> "$branches"
        printf '%s|%s|/%s/\n' "$branch" "$branch" "$branch"
    done
    while IFS= read -r tag; do
        printf 'tag:%s|%s|/%s/\n' "$tag" "$tag" "$tag"
    done < <(git tag --list 'v[0-9]*' --sort=-version:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true)
} > "$versions"

build_ref() {
    local ref=$1
    local path=$2
    local key=$3
    local label=$4
    local site_url=$5
    ./site/build.sh "$ref" "$path" "$key" "$label" "$versions" "$site_url"
}

while IFS= read -r tag; do
    build_ref "$tag" "$public/$tag" "tag:$tag" "$tag" "/$tag/"
done <<< "$tags"

while IFS='|' read -r branch ref; do
    build_ref "$ref" "$public/$branch" "$branch" "$branch" "/$branch/"
done < "$branches"

build_ref "$latest" "$public/root" latest "Latest ($latest)" "/"
cp -a "$public/root/." "$public/"
rm -rf "$public/root"

rm -rf "$repo/site/public"
mv "$public" "$repo/site/public"
printf 'Built docs for %s stable tags, main, and forge. Latest: %s\n' "$tag_count" "$latest"
