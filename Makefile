BINARY      := temporal
PKG         := ./...
INSTALL_DIR ?= $(HOME)/.local/bin

.PHONY: build vet test lint tidy run clean install uninstall

build:
	go build -trimpath -ldflags="-s -w" -o $(BINARY) .

vet:
	go vet $(PKG)

test:
	go test -count=1 $(PKG)

lint: vet
	gofmt -l . | tee /tmp/gofmt.out
	@! [ -s /tmp/gofmt.out ]

tidy:
	go mod tidy

run: build
	./$(BINARY)

# install moves the built binary into $(INSTALL_DIR) (default ~/.local/bin).
# Override with:
#   make install INSTALL_DIR=/usr/local/bin
install: build
	mkdir -p $(INSTALL_DIR)
	mv $(BINARY) $(INSTALL_DIR)/$(BINARY)

uninstall:
	rm -f "$(INSTALL_DIR)/$(BINARY)"

clean:
	rm -f $(BINARY)
	rm -rf dist
