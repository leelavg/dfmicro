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
books=()
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
    books+=("$public/$tag")
done <<< "$tags"

while IFS='|' read -r branch ref; do
    build_ref "$ref" "$public/$branch" "$branch" "$branch" "/$branch/"
    books+=("$public/$branch")
done < "$branches"

build_ref "$latest" "$public/root" latest "Latest ($latest)" "/"
books+=("$public/root")

first_book=${books[0]}
sed_rules="$build_dir/shared-assets.sed"
: > "$sed_rules"

while IFS= read -r relative; do
    source="$first_book/$relative"
    common=true
    for book in "${books[@]}"; do
        if [[ ! -f "$book/$relative" ]] || ! cmp -s "$source" "$book/$relative"; then
            common=false
            break
        fi
    done
    [[ "$common" == true ]] || continue

    mkdir -p "$public/_shared/$(dirname "$relative")"
    cp "$source" "$public/_shared/$relative"

    escaped=${relative//./\\.}
    printf 's#((href|src)=")([.][.]/)*%s#\\1/_shared/%s#g\n' "$escaped" "$relative" >> "$sed_rules"
    for book in "${books[@]}"; do
        rm "$book/$relative"
    done
done < <(
    {
        for asset in "$first_book"/*.js "$first_book"/*.css "$first_book"/favicon* "$first_book"/apple-touch-icon.png; do
            [[ -f "$asset" ]] || continue
            relative=${asset#"$first_book"/}
            [[ "$relative" == searchindex-* ]] && continue
            printf '%s\n' "$relative"
        done
        for directory in css fonts; do
            [[ -d "$first_book/$directory" ]] || continue
            find "$first_book/$directory" -type f -print | while IFS= read -r asset; do
                printf '%s\n' "${asset#"$first_book"/}"
            done
        done
    } | sort -u
)

if [[ -s "$sed_rules" ]]; then
    for book in "${books[@]}"; do
        while IFS= read -r html; do
            sed -E -f "$sed_rules" "$html" > "$html.tmp"
            mv "$html.tmp" "$html"
        done < <(find "$book" -type f -name '*.html' -print)
    done
fi

cp -a "$public/root/." "$public/"
rm -rf "$public/root"

rm -rf "$repo/site/public"
mv "$public" "$repo/site/public"
printf 'Built docs for %s stable tags, main, and forge. Latest: %s\n' "$tag_count" "$latest"
