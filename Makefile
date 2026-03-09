APP_NAME := debian-updater
BUILD_CMD := CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static" -s -w'

VERSIONS := jessie stretch buster bullseye bookworm

.PHONY: all build clean test go-test lint test-dry $(addsuffix -dry, $(VERSIONS)) $(addsuffix -full, $(VERSIONS))

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

test-dry: $(addsuffix -dry, $(VERSIONS))

$(addsuffix -dry, $(VERSIONS)): %-dry: build
	@echo ""
	@echo "=========================================================="
	@echo "=== Running DRY RUN test on docker image: debian:$* ==="
	@echo "=========================================================="
	docker run --rm --name test-updater-$* -v $(PWD)/$(APP_NAME):/$(APP_NAME) debian:$* /$(APP_NAME) --dry-run

$(addsuffix -full, $(VERSIONS)): %-full: build
	@echo ""
	@echo "=========================================================="
	@echo "=== Running FULL UPGRADE on docker image: debian:$* ==="
	@echo "=== WARNING: This will take time and download data! ==="
	@echo "=========================================================="
	docker run --rm --name test-updater-$* -v $(PWD)/$(APP_NAME):/$(APP_NAME) debian:$* /$(APP_NAME)
