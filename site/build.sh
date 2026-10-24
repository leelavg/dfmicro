#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 6 ]]; then
    printf 'Usage: %s REF OUTPUT CURRENT_KEY CURRENT_LABEL VERSIONS_FILE SITE_URL\n' "$0" >&2
    exit 2
fi

ref=$1
output=$2
current=$3
current_label=$4
versions_file=$5
site_url=$6
repo=$(git rev-parse --show-toplevel)

case "$current" in
    latest) source_ref=$ref ;;
    tag:*) source_ref=${current#tag:} ;;
    main | forge) source_ref=$current ;;
    *) printf 'Unknown docs version: %s\n' "$current" >&2; exit 2 ;;
esac

mdbook=${MDBOOK:-mdbook}
if ! command -v "$mdbook" >/dev/null 2>&1; then
    printf 'mdBook is required. Run make install-mdbook.\n' >&2
    exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
book="$tmp/book"
src="$book"
theme="$book/theme"
mkdir -p "$src" "$theme"

sed -e "s|__SITE_URL__|$site_url|" \
    -e "s|__SOURCE_REF__|$source_ref|" \
    "$repo/site/book.toml" > "$book/book.toml"
cp "$repo/site/site.css" "$book/site.css"
cp "$repo/site/version.js" "$book/version.js"
cp "$repo/site/head.hbs" "$theme/head.hbs"
cp "$repo/internal/docs/assets/dfmicro.svg" "$theme/favicon.svg"
cp "$repo/internal/docs/assets/favicon-32x32.png" "$theme/favicon.png"
cp "$repo/internal/docs/assets/favicon-16x16.png" "$src/favicon-16x16.png"
cp "$repo/internal/docs/assets/apple-touch-icon.png" "$src/apple-touch-icon.png"
cp "$repo/internal/docs/assets/favicon.ico" "$src/favicon.ico"

copy_ref_file() {
    local path=$1
    local destination="$src/$path"
    mkdir -p "$(dirname "$destination")"

    if [[ "$ref" == working-tree ]]; then
        [[ -f "$repo/$path" ]] || return 1
        cp "$repo/$path" "$destination"
    else
        git cat-file -e "$ref:$path" 2>/dev/null || return 1
        git show "$ref:$path" > "$destination"
    fi
}

{
    printf '# Summary\n\n'
    while IFS='|' read -r title path; do
        [[ -n "$title" && -n "$path" ]] || continue
        case "$path" in
            /*|../*|*/../*|*/..) printf 'Invalid docs source path: %s\n' "$path" >&2; exit 1 ;;
        esac

        if copy_ref_file "$path"; then
            printf -- '- [%s](%s)\n' "$title" "$path"
        else
            printf 'Skipping %s, missing from %s\n' "$path" "$ref" >&2
        fi
    done < "$repo/site/pages.tsv"
} > "$src/SUMMARY.md"

while IFS= read -r path; do
    [[ -n "$path" ]] || continue
    copy_ref_file "$path" || true
done < "$repo/site/assets.txt"

{
    printf '<nav id="dfmicro-version" aria-label="Documentation version">\n'
    printf '<details><summary>%s</summary><ul>\n' "$current_label"
    while IFS='|' read -r key label href; do
        [[ -n "$key" && -n "$label" && -n "$href" ]] || continue
        if [[ "$key" == "$current" ]]; then
            printf '<li><span aria-current="page">%s</span></li>\n' "$label"
        else
            printf '<li><a href="%s">%s</a></li>\n' "$href" "$label"
        fi
    done < "$versions_file"
    printf '</ul></details>\n</nav>\n'
} > "$theme/header.hbs"

mkdir -p "$(dirname "$output")"
output="$(cd "$(dirname "$output")" && pwd)/$(basename "$output")"
rm -rf "$output"
"$mdbook" build "$book" --dest-dir "$output"
