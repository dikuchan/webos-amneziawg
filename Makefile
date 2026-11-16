.PHONY: all binaries package verify test clean

all: package verify

binaries:
	./scripts/build-binaries.sh

package: binaries
	./scripts/package.sh

verify:
	./scripts/verify-release.sh

test:
	go test -race ./uploader/wg-upload.go ./uploader/wg-upload_test.go
	go vet ./uploader/wg-upload.go
	./scripts/test-dns.sh

clean:
	rm -rf dist
