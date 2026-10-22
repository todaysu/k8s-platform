.PHONY: test dev-backend dev-frontend build-frontend build-image

GO ?= $(firstword $(wildcard /opt/homebrew/bin/go) go)

test:
	$(GO) test ./...
	cd web && npm test -- --run

dev-backend:
	@set -a; if [ -f .env ]; then . ./.env; fi; set +a; \
		ENABLE_POD_EXEC=$${ENABLE_POD_EXEC:-true} $(GO) run ./cmd/platform

dev-frontend:
	cd web && npm run dev

build-frontend:
	cd web && npm run build

build-image:
	docker build -t ftc-platform:dev .
