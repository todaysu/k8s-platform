.PHONY: test dev-backend dev-frontend build-frontend build-image build-image-amd64 build-image-arm64 build-image-multi package-images push-image-amd64 kind-create kind-load kind-deploy

GO ?= $(firstword $(wildcard /opt/homebrew/bin/go) go)

# Image coordinates. Override on the command line, e.g.
#   make build-image-amd64 REGISTRY=registry.cmbchina.com/ops IMAGE_TAG=0.2.0
REGISTRY ?= your-registry.example.com
IMAGE_NAME ?= ftc-platform
VERSION ?= v1.0.0
IMAGE_TAG ?= $(VERSION)
FULL_IMAGE ?= $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
LOCAL_IMAGE ?= ftc-platform:$(VERSION)
KIND_NODE_IMAGE ?= kindest/node:v1.24.17

# Build-time mirrors. The Dockerfile defaults already point at mainland-China
# reachable mirrors; override for other networks.
GOPROXY ?= https://goproxy.cn,https://goproxy.io,direct
NPM_REGISTRY ?= https://registry.npmmirror.com
BUILD_ARGS ?= --build-arg VERSION=$(VERSION) --build-arg GOPROXY=$(GOPROXY) --build-arg NPM_REGISTRY=$(NPM_REGISTRY)

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

# Build a single-architecture x86_64 image that runs on amd64 nodes.
# The Go binary is cross-compiled with CGO disabled, so no emulator or
# cross toolchain is required on an arm64 workstation.
build-image-amd64:
	docker buildx build --platform linux/amd64 $(BUILD_ARGS) -t $(FULL_IMAGE) --load .

# Build a single-architecture ARM64 image for ARM Kubernetes nodes and local
# Apple Silicon Kind clusters.
build-image-arm64:
	docker buildx build --platform linux/arm64 $(BUILD_ARGS) -t $(FULL_IMAGE) --load .

# Build a multi-architecture manifest for both amd64 and arm64 clusters.
# The result is pushed directly to the registry; --load cannot hold a
# manifest list in the local image store.
build-image-multi:
	docker buildx build --platform linux/amd64,linux/arm64 $(BUILD_ARGS) -t $(FULL_IMAGE) --push .

# Convenience: build the x86_64 image and push it in one step.
push-image-amd64: build-image-amd64
	docker push $(FULL_IMAGE)

# Produce two Docker archives for offline x86_64 and ARM64 delivery. The
# archives are release artifacts and intentionally stay outside Git.
package-images:
	VERSION=$(VERSION) LOCAL_IMAGE=$(LOCAL_IMAGE) GO=$(GO) ./scripts/package-images.sh

kind-create:
	kind create cluster --name ftc-v1 --image $(KIND_NODE_IMAGE) --config deploy/kind/cluster.yaml

kind-load:
	kind load docker-image $(LOCAL_IMAGE)-arm64 --name ftc-v1
	kind load docker-image mysql:8.0 --name ftc-v1

kind-deploy:
	kubectl --context kind-ftc-v1 apply -f deploy/kind/mysql.yaml
	kubectl --context kind-ftc-v1 -n ftc-platform rollout status statefulset/platform-mysql --timeout=5m
	sed 's|REPLACE_WITH_PLATFORM_IMAGE|$(LOCAL_IMAGE)-arm64|' data/delivery/install.yaml | kubectl --context kind-ftc-v1 apply -f -
