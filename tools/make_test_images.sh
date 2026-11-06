#!/bin/sh
# Creates every image listed in docs/testing.md in test-images/.
set -eu
cd "$(dirname "$0")/.."
go build ./cmd/ahic
mkdir -p test-images
rm -f test-images/*.img
number=0
make_images() { # label size tos bus
    for driver in ahdi cbhd icd; do
        number=$((number + 1))
        file=$(printf 'test-images/%02d-%s-%s.img' "$number" "$1" "$driver")
        ./ahic create --size "$2" --tos "$3" --bus "$4" --driver "$driver" "$file" >/dev/null
        ./ahic verify "$file" >/dev/null
        echo "$file"
    done
}
make_images tos102-st-acsi 64 1.02 acsi
make_images tos104-st-acsi 64 1.04 acsi
make_images tos206-st-acsi 64 2.06 acsi
make_images tos306-tt-acsi 64 3.06 acsi
make_images tos306-tt-scsi 64 3.06 scsi
make_images smallest-4mb 4 1.04 acsi
make_images small-bgm-40mb 40 1.04 acsi
make_images tos102-limit-399mb 399 1.02 acsi
make_images 8k-sectors-1024mb 1024 1.04 acsi
