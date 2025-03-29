.PHONY: build clean

# Variables
PLUGIN_NAME = contract-invocations.so
GO = go
BUILD_FLAGS = -buildmode=plugin

# Main targets
all: build

build:
	@echo "Building Soroban contract invocations processor plugin..."
	$(GO) build $(BUILD_FLAGS) -o $(PLUGIN_NAME) .

test:
	@echo "Running tests..."
	$(GO) test -v ./...

clean:
	@echo "Cleaning up..."
	rm -f $(PLUGIN_NAME)

lint:
	@echo "Running linters..."
	golangci-lint run

# Development helpers
dev: clean build

watch:
	@echo "Watching for changes and rebuilding..."
	find . -name "*.go" | entr -c make build 