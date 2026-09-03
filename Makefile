.PHONY: clean build ui test

GOBIN ?= $(shell go env GOPATH)/bin

clean:
	go clean ./...
	rm -f $(GOBIN)/*

build:
	go install ./...

ui:
	cd frontend && npm ci && npm run build

test:
	go test ./... -count=1
	go vet ./...
