APP_NAME := debian-updater
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_CMD := CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static" -s -w'
GOARCH ?= amd64
ifneq ($(GOARCH),amd64)
	BUILD_CMD := GOARCH=$(GOARCH) $(BUILD_CMD)
endif

VERSIONS := jessie stretch buster bullseye bookworm

.PHONY: all build clean test go-test lint vuln sbom sbom-scan release-check vagrant-up vagrant-dry vagrant-full vagrant-destroy

all: build

build:
	@echo "==> Building static binary..."
	$(BUILD_CMD) -o $(APP_NAME) .
	@echo "==> Build complete: $(APP_NAME)"

clean:
	@echo "==> Cleaning up..."
	rm -f $(APP_NAME)

go-test:
	@echo "==> Running go test..."
	go test ./...

lint:
	@echo "==> Running golangci-lint..."
	golangci-lint run

vuln:
	@echo "==> Running govulncheck..."
	govulncheck ./...

sbom:
	@echo "==> Generating SBOM (CycloneDX JSON) for $(APP_NAME) $(VERSION)..."
	syft . --source-name $(APP_NAME) --source-version $(VERSION) -o cyclonedx-json=sbom.json
	@echo "==> SBOM written: sbom.json"

sbom-scan: sbom
	@echo "==> Scanning SBOM with grype (fail on high/critical)..."
	grype sbom.json --fail-on high

release-check: lint test vuln sbom-scan
	@echo "==> Release checks passed"

test: go-test

vagrant-up:
	@echo "==> Building static binary for Vagrant (386)..."
	$(MAKE) build GOARCH=386
	@echo "==> Starting Vagrant VM (libvirt)..."
	vagrant up --provider=libvirt

# The Vagrant box boots an EOL Debian release (buster) so the tool can
# exercise its archive.debian.org path end-to-end. --trust-eol-archive
# acknowledges the [trusted=yes] mirror configuration; --insecure-tls lets
# the old CA bundle fall back to HTTP when modern TLS negotiation fails.
VAGRANT_FLAGS := --trust-eol-archive --insecure-tls

vagrant-dry: vagrant-up
	@echo "==> Running DRY RUN test in Vagrant..."
	vagrant ssh -c "sudo /debian-updater/$(APP_NAME) --dry-run $(VAGRANT_FLAGS)"

vagrant-full: vagrant-up
	@echo "==> Running FULL UPGRADE in Vagrant..."
	vagrant ssh -c "sudo /debian-updater/$(APP_NAME) $(VAGRANT_FLAGS)"

vagrant-destroy:
	@echo "==> Destroying Vagrant VM..."
	vagrant destroy -f
