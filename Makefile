.PHONY: build clean run dev help

BINARY_NAME := wg-mgmt-daemon
OUTPUT_DIR  := bin
CMD_DIR     := ./cmd/mgmt-daemon

GOOS        ?= linux
GOARCH      ?= amd64
CGO_ENABLED ?= 0
LDFLAGS     := -s -w

help:
	@echo "WireGuard Management Layer"
	@echo ""
	@echo "Usage:"
	@echo "  make build     Cross-compile for Linux amd64"
	@echo "  make build-win Cross-compile for Windows amd64 (testing)"
	@echo "  make run       Run locally (for testing, requires wg tools)"
	@echo "  make clean     Remove build artifacts"
	@echo "  make vet       Run go vet"
	@echo ""

build:
	@echo "Building $(BINARY_NAME) for $(GOOS)/$(GOARCH)..."
	@mkdir -p $(OUTPUT_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
		go build -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "Done: $(OUTPUT_DIR)/$(BINARY_NAME)"
	@ls -lh $(OUTPUT_DIR)/$(BINARY_NAME)

build-win:
	@echo "Building $(BINARY_NAME) for windows/amd64..."
	@mkdir -p $(OUTPUT_DIR)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
		go build -ldflags="$(LDFLAGS)" -o $(OUTPUT_DIR)/$(BINARY_NAME).exe $(CMD_DIR)
	@echo "Done: $(OUTPUT_DIR)/$(BINARY_NAME).exe"

run:
	@go run $(CMD_DIR)

clean:
	@rm -rf $(OUTPUT_DIR)
	@echo "Cleaned"

vet:
	@go vet ./...
