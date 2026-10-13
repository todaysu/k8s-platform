.PHONY: test dev-backend dev-frontend build-frontend

GO ?= $(firstword $(wildcard /opt/homebrew/bin/go) go)

test:
	$(GO) test ./...
	cd web && npm test -- --run

dev-backend:
	$(GO) run ./cmd/platform

dev-frontend:
	cd web && npm run dev

build-frontend:
	cd web && npm run build
