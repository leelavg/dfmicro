#!/bin/sh

set -eu

repo="leelavg/dfmicro"
install_dir="${INSTALL_DIR:-$HOME/.local/bin}"
version="${VERSION:-latest}"

log() {
    echo "dfmicro: $*" >&2
}

usage() {
    echo "Usage: install.sh [--list]"
    echo ""
    echo "  --list    list available releases without installing"
}

die() {
    echo "$1" >&2
    exit 1
}

detect_platform() {
    case "$(uname -s)" in
        Linux) os=linux ;;
        Darwin) os=darwin ;;
        *) die "unsupported operating system: $(uname -s)" ;;
    esac

    case "$(uname -m)" in
        x86_64 | amd64) arch=amd64 ;;
        arm64 | aarch64) arch=arm64 ;;
        *) die "unsupported architecture: $(uname -m)" ;;
    esac

    log "target platform: ${os}/${arch}"
}

check_dependencies() {
    missing=""
    for command do
        if ! command -v "$command" >/dev/null 2>&1; then
            if [ -n "$missing" ]; then
                missing="$missing $command"
            else
                missing=$command
            fi
        fi
    done

    [ -z "$missing" ] || die "required commands not found:$missing"
}

check_checksum_dependency() {
    if ! command -v sha256sum >/dev/null 2>&1 &&
        ! command -v shasum >/dev/null 2>&1; then
        die "required command not found: sha256sum or shasum"
    fi
}

list_releases() {
    check_dependencies curl jq column
    curl -fsSL "https://api.github.com/repos/$repo/releases?per_page=100" |
        jq -r '.[] | "\(.tag_name)\t\(.published_at)"' |
        column -t
}

get_asset_url() {
    asset_name=$1
    printf '%s\n' "$release" |
        jq -r --arg name "$asset_name" '
            .assets[] | select(.name == $name) | .browser_download_url
        ' |
        head -n 1
}

find_archive_name() {
    printf '%s\n' "$release" |
        jq -r --arg os "$os" --arg arch "$arch" '
            .assets[]
            | select(.name | test("^dfmicro_.*_" + $os + "_" + $arch + "\\.tar\\.gz$"))
            | .name
        ' |
        head -n 1
}

find_checksum_name() {
    printf '%s\n' "$release" |
        jq -r '
            .assets[]
            | select(.name | test("^dfmicro_.*_checksums\\.txt$"))
            | .name
        ' |
        head -n 1
}

verify_checksum() {
    archive_name=$1
    archive_path=$2
    checksum_path=$3
    expected=$(awk -v file="$archive_name" \
        '$2 == file || $2 == "*" file { print $1; exit }' "$checksum_path")
    [ -n "$expected" ] || die "checksum not found for $archive_name"

    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$archive_path" | awk '{print $1}')
    else
        actual=$(shasum -a 256 "$archive_path" | awk '{print $1}')
    fi

    [ "$expected" = "$actual" ] || die "checksum verification failed for $archive_name"
}

install_binary() {
    mkdir -p "$install_dir"
    install -m 0755 "$work_dir/dfmicro" "$install_dir/dfmicro"
    echo "installed dfmicro to $install_dir/dfmicro"
}

main() {
    case "${1:-}" in
        "") ;;
        --list) list_releases; return ;;
        --help | -h) usage; return ;;
        *) die "unknown option: $1" ;;
    esac

    detect_platform
    log "checking dependencies"
    check_dependencies curl jq tar install head mktemp uname mkdir rm awk
    check_checksum_dependency

    if [ "$version" = latest ]; then
        release_url="https://api.github.com/repos/$repo/releases/latest"
    else
        case "$version" in
            v*) tag=$version ;;
            *) tag="v$version" ;;
        esac
        release_url="https://api.github.com/repos/$repo/releases/tags/$tag"
    fi
    log "reading release metadata"
    release=$(curl -fsSL "$release_url")
    archive_name=$(find_archive_name)
    [ -n "$archive_name" ] || die "no release archive found for ${os}/${arch}"
    archive_url=$(get_asset_url "$archive_name")
    checksum_name=$(find_checksum_name)
    checksum_url=$(get_asset_url "$checksum_name")
    [ -n "$archive_url" ] || die "release URL not found for $archive_name"
    [ -n "$checksum_name" ] || die "release checksums not found"
    [ -n "$checksum_url" ] || die "release URL not found for $checksum_name"

    work_dir=$(mktemp -d)
    trap 'rm -rf "$work_dir"' 0
    log "downloading $archive_name"
    curl -fsSL "$archive_url" -o "$work_dir/dfmicro.tar.gz"
    log "downloading $checksum_name"
    curl -fsSL "$checksum_url" -o "$work_dir/checksums.txt"
    log "verifying archive checksum"
    verify_checksum "$archive_name" "$work_dir/dfmicro.tar.gz" "$work_dir/checksums.txt"

    log "extracting archive"
    tar -xzf "$work_dir/dfmicro.tar.gz" -C "$work_dir"
    [ -f "$work_dir/dfmicro" ] || die "release archive does not contain dfmicro"
    log "installing to $install_dir"
    install_binary
}

main "$@"
