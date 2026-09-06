BIN := bin/dav

.PHONY: build test lint run clean

build:
	go build -o $(BIN) ./cmd/dav

test:
	go test ./...

lint:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet ./...

run:
	go run ./cmd/dav

clean:
	rm -rf bin
