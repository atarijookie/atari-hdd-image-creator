#!/bin/sh
# Builds ahic into dist/ and packages each build with the README and licence.
#
# Usage: tools/build_release.sh [all|linux|windows|macos]...
# VERSION sets the version ahic reports, and defaults to the git description.
set -eu
cd "$(dirname "$0")/.."

version=${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}

build() { # os arch
    name="ahic-$1-$2"
    extension=
    [ "$1" = windows ] && extension=.exe
    binary="dist/$name$extension"
    GOOS=$1 GOARCH=$2 CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X main.version=$version" -o "$binary" ./cmd/ahic

    package="dist/ahic-$version-$1-$2"
    rm -rf "$package"
    mkdir "$package"
    cp "$binary" "$package/ahic$extension"
    cp README.md LICENSE "$package/"
    if [ "$1" = windows ]; then
        (cd dist && rm -f "${package#dist/}.zip" && python3 -m zipfile -c "${package#dist/}.zip" "${package#dist/}")
        echo "$package.zip"
    else
        tar -czf "$package.tar.gz" -C dist "${package#dist/}"
        echo "$package.tar.gz"
    fi
    rm -rf "$package"
}

[ $# -gt 0 ] || set -- all
mkdir -p dist
for target in "$@"; do
    case "$target" in
    all)
        build linux amd64
        build linux arm64
        build windows amd64
        build darwin amd64
        build darwin arm64
        ;;
    linux)
        build linux amd64
        build linux arm64
        ;;
    windows) build windows amd64 ;;
    macos)
        build darwin amd64
        build darwin arm64
        ;;
    *)
        echo "unknown target: $target" >&2
        exit 1
        ;;
    esac
done
