APP_NAME := debian-updater
BUILD_CMD := CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static" -s -w'
GOARCH ?= amd64
ifneq ($(GOARCH),amd64)
	BUILD_CMD := GOARCH=$(GOARCH) $(BUILD_CMD)
endif

VERSIONS := jessie stretch buster bullseye bookworm

.PHONY: all build clean test go-test lint vagrant-up vagrant-dry vagrant-full vagrant-destroy

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

test: go-test

vagrant-up:
	@echo "==> Building static binary for Vagrant (386)..."
	$(MAKE) build GOARCH=386
	@echo "==> Starting Vagrant VM (libvirt)..."
	vagrant up --provider=libvirt

vagrant-dry: vagrant-up
	@echo "==> Running DRY RUN test in Vagrant..."
	vagrant ssh -c "sudo /debian-updater/$(APP_NAME) --dry-run"

vagrant-full: vagrant-up
	@echo "==> Running FULL UPGRADE in Vagrant..."
	vagrant ssh -c "sudo /debian-updater/$(APP_NAME)"

vagrant-destroy:
	@echo "==> Destroying Vagrant VM..."
	vagrant destroy -f
