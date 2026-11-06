#!/bin/sh
# Creates every image listed in docs/testing.md in test-images/. Images that
# already exist are left alone, so delete one to rebuild it.
set -eu
cd "$(dirname "$0")/.."
go build ./cmd/ahic
mkdir -p test-images
number=0
make_images() { # label size tos bus boot-size
    for driver in ahdi cbhd icd; do
        number=$((number + 1))
        file=$(printf 'test-images/%02d-%s-%s.img' "$number" "$1" "$driver")
        [ -e "$file" ] && continue
        ./ahic create --size "$2" --tos "$3" --bus "$4" --boot-size "$5" --driver "$driver" "$file" >/dev/null
        ./ahic verify "$file" >/dev/null
        echo "$file"
    done
}
make_images tos102-st-acsi 64 1.02 acsi 15
make_images tos104-st-acsi 64 1.04 acsi 15
make_images tos206-st-acsi 64 2.06 acsi 15
make_images tos306-tt-acsi 64 3.06 acsi 15
make_images tos306-tt-scsi 64 3.06 scsi 15
make_images smallest-4mb 4 1.04 acsi 15
make_images small-bgm-40mb 40 1.04 acsi 15
make_images tos102-limit-399mb 399 1.02 acsi 15
make_images 8k-sectors-1024mb 1024 1.04 acsi 15
number=28 # 28 was a one-off AHDI test
make_images tt-bgm-boot-128mb-acsi 512 3.06 acsi 128
make_images tt-bgm-boot-128mb-scsi 512 3.06 scsi 128
make_images tt-bgm-boot-512mb-acsi 1024 3.06 acsi 512
make_images tt-bgm-boot-512mb-scsi 1024 3.06 scsi 512
make_images st-bgm-boot-128mb-tos102 512 1.02 acsi 128
make_images st-bgm-boot-128mb-tos206 512 2.06 acsi 128
