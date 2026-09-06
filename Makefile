BIN := bin/dav

EDAV_ADMIN_PASSWORD ?= devpassword
EDAV_DB_PATH ?= edav.db

.PHONY: build test lint run clean

build:
	go build -o $(BIN) ./cmd/dav

test:
	go test ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

run:
	EDAV_ADMIN_PASSWORD=$(EDAV_ADMIN_PASSWORD) EDAV_DB_PATH=$(EDAV_DB_PATH) go run ./cmd/dav

clean:
	rm -rf bin
