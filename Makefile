BINARY    := bkp
PKG       := ./...
BUILD_DIR := bin
DIST_DIR  := dist
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)

.PHONY: all build run test vet fmt tidy clean release

all: build

build:
	mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) .

run: build
	./$(BUILD_DIR)/$(BINARY) --config examples/demo/config.yaml

test:
	go test $(PKG)

vet:
	go vet $(PKG)

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR)

# Cross-compiles release binaries (pure Go, no CGO needed), tars them up, and
# writes a checksums.txt, mirroring what .github/workflows/release.yml does
# on a tag push.
release:
	mkdir -p $(DIST_DIR)
	for platform in linux/amd64 linux/arm64 darwin/arm64; do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		out=$(BINARY)-$$os-$$arch; \
		echo "building $$out ($(VERSION))"; \
		GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(DIST_DIR)/$$out .; \
		tar -C $(DIST_DIR) -czf $(DIST_DIR)/$$out.tar.gz $$out; \
	done
	cd $(DIST_DIR) && (command -v sha256sum >/dev/null && sha256sum *.tar.gz || shasum -a 256 *.tar.gz) > checksums.txt
