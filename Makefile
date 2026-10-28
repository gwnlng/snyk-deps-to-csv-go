SHELL := /bin/bash

GOCMD=go
GOMOD=$(GOCMD) mod
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test

all:
	$(info  "completed running make file for golang project")
fmt:
	@go fmt ./...
tidy:
	$(GOMOD) tidy -v
lint:
	golangci-lint run ./...
test:
	$(GOTEST) ./... -coverprofile cp.out
build:
	$(GOBUILD) -o snyk-deps-to-csv cmd/snyk-deps-to-csv/main.go
build-windows:
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o snyk-deps-to-csv.exe cmd/snyk-deps-to-csv/main.go
.PHONY: install-req fmt test lint build tidy imports
