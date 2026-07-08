BINARY  := bkp
PKG     := ./...
BUILD_DIR := bin

.PHONY: all build run test vet fmt tidy clean

all: build

build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY) .

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
	rm -rf $(BUILD_DIR)
