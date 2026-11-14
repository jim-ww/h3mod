VERSION := $(shell cat VERSION)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

.PHONY: build test bundles release publish clean

# h3mod for this system; it finds its tools in PATH (see README)
build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o h3mod .

# tests need the tools and hash lists from the dev shell
test:
	nix develop -c go test -count=1 ./...

# Linux and Windows archives with all tools, in result-bundle*/
bundles:
	nix build .#bundle -o result-bundle
	nix build .#bundle-windows -o result-bundle-windows

# versioned archives and checksums in dist/, ready to upload
release: test bundles
	rm -rf $(DIST) && mkdir -p $(DIST)
	cp result-bundle/* result-bundle-windows/* $(DIST)/
	chmod u+w $(DIST)/*
	cd $(DIST) && sha256sum h3mod-* > checksums.txt

# GitHub release v$(VERSION) from dist/ (needs gh and a pushed tag)
publish: release
	gh release create v$(VERSION) --title 'h3mod $(VERSION)' --generate-notes $(DIST)/*

clean:
	rm -rf $(DIST) h3mod h3mod.exe result result-bundle result-bundle-windows
