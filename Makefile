GO ?= /tmp/go1.26.5/bin/go

.PHONY: test vet run migrate

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

run:
	$(GO) run ./cmd/api

migrate:
	$(GO) run ./cmd/migrate
