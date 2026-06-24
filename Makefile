BINARY  := ccgate
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"

.PHONY: build install test vet fmt clean

build:
	go build $(LDFLAGS) -o $(BINARY) .

install: build
	install -d $(HOME)/.local/bin
	install -m 0755 $(BINARY) $(HOME)/.local/bin/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
