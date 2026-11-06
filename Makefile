.PHONY: build run test clean

# Pass arguments with: make run ARGS="info disk.img"
ARGS ?=

build:
	go build ./cmd/ahic

run: build
	./ahic $(ARGS)

test:
	go test ./...

clean:
	go clean
	rm -rf ahic ahic.exe dist
