.PHONY: build run test clean windows macos dist

# Pass arguments with: make run ARGS="info disk.img"
ARGS ?=

build:
	go build ./cmd/ahic

run: build
	./ahic $(ARGS)

test:
	go test ./...

windows:
	tools/build_release.sh windows

macos:
	tools/build_release.sh macos

dist:
	tools/build_release.sh all

clean:
	go clean
	rm -rf ahic ahic.exe dist
