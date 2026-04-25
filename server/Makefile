# Makefile for the Go log server

# Variables
BINARY_NAME=temporal
INSTALL_PATH=$(HOME)/.local/bin

# Default target
all: build

# Build the Go binary
build:
	@echo "Building the application..."
	go build -o $(BINARY_NAME) main.go

# Run the tests
test:
	@echo "Running tests..."
	go test -v ./...

# Install the binary
install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_PATH)..."
	@mv $(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME)

# Uninstall the binary
uninstall:
	@echo "Uninstalling $(BINARY_NAME) from $(INSTALL_PATH)..."
	@rm -f $(INSTALL_PATH)/$(BINARY_NAME)

# Clean up build artifacts
clean:
	@echo "Cleaning up..."
	go clean
	rm -f $(BINARY_NAME)

.PHONY: all build test install uninstall clean
