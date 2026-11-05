#!/bin/sh
# Builds ahic for every supported platform into dist/.
set -eu
cd "$(dirname "$0")/.."
rm -rf dist
for target in linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64; do
    os=${target%/*}
    arch=${target#*/}
    extension=
    [ "$os" = windows ] && extension=.exe
    output="dist/ahic-$os-$arch$extension"
    GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "$output" ./cmd/ahic
    echo "$output"
done
