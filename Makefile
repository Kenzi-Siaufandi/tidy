.PHONY: all build test clean

BINARY_NAME=tidy

all: test build

build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY_NAME) main.go

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME)
