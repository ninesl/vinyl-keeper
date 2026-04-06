# Vinyl Keeper - Podman image-based workflow

IMAGE_APP = vinyl-keeper:app
IMAGE_SERVICE = vinyl-keeper:image-service
APP_DOCKERFILE = Dockerfile
APP_BIN_DIR = app/build
APP_BIN = $(APP_BIN_DIR)/vinyl-keeper
APP_GOOS ?= linux
APP_GOARCH ?= amd64

# Optional local overrides from .env
-include .env

VPS_USER ?= $(VINYL_KEEPER_VPS_USER)
VPS_HOST ?= $(VINYL_KEEPER_VPS_IP)
VPS_PROD_DIR = /opt/vinyl-keeper
DETECTED_LAN_IP := $(shell ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -n 1)
LAN_IP := $(if $(VINYL_KEEPER_LAN_IP),$(VINYL_KEEPER_LAN_IP),$(DETECTED_LAN_IP))

ENABLE_TLS ?= true
TLS_CERT ?= certs/dev.crt
TLS_KEY ?= certs/dev.key
IMAGE_SERVICE_HOST ?= 127.0.0.1
IMAGE_SERVICE_PORT ?= 8081
IMAGE_SERVICE_ENDPOINT ?= /embed
IMAGE_SERVICE_HEALTH_ENDPOINT ?= /health
IMAGE_SERVICE_WAIT_RETRIES ?= 60
IMAGE_SERVICE_WAIT_SECONDS ?= 1
EMBED_MODEL_PATH ?= models/ViT-B-32__openai/visual/model.onnx
EMBED_MODEL_FAMILY ?= clip
EMBED_DIM ?= 512
EMBED_IMAGE_SIZE ?= 224

COMPOSE_LOCAL = podman-compose.local.yml
COMPOSE_PROD = podman-compose.prod.yml

DEPLOY_DIR = deploy
DEPLOY_APP_TAR = $(DEPLOY_DIR)/app.tar
DEPLOY_SERVICE_TAR = $(DEPLOY_DIR)/service.tar

help:
	@echo "Public commands:"
	@echo "  make help        Show this help"
	@echo "  make build       Build podman images (builds Go app from app/)"
	@echo "  make dev         Run containerized local HTTPS stack (includes check-images, certs)"
	@echo "  make dev-native  Run non-container local stack from app/ (includes certs)"
	@echo "  make dev-down    Stop local containerized stack"
	@echo "  make cicd        Build, ship tarballs, and deploy to VPS (includes require-vps-env, save)"
	@echo "  make show-config Show resolved .env/network values"
	@echo ""
	@echo "Internal commands (normally called by public commands):"
	@echo "  make build-bin       Build Go binary from app/ ($(APP_BIN)); used by: build"
	@echo "  make check-images    Verify required images exist; used by: dev"
	@echo "  make certs           Generate local self-signed TLS certs; used by: dev, dev-native"
	@echo "  make require-vps-env Validate VPS env vars; used by: cicd"
	@echo "  make save            Build and export image tarballs to $(DEPLOY_DIR); used by: cicd"
	@echo "  make clean           Remove local build/deploy artifacts and stop local stack"
	@echo ""
	@echo "See .env/.env.example for all configurable variables"

show-config:
	@echo "VPS_USER=$(if $(VPS_USER),$(VPS_USER),<unset>)"
	@echo "VPS_HOST=$(if $(VPS_HOST),$(VPS_HOST),<unset>)"
	@echo "VINYL_KEEPER_LAN_IP=$(if $(VINYL_KEEPER_LAN_IP),$(VINYL_KEEPER_LAN_IP),<unset>)"
	@echo "DETECTED_LAN_IP=$(if $(DETECTED_LAN_IP),$(DETECTED_LAN_IP),<none>)"
	@echo "LAN_IP(in use)=$(if $(LAN_IP),$(LAN_IP),<none>)"
	@echo "IMAGE_SERVICE_HEALTH_ENDPOINT=$(IMAGE_SERVICE_HEALTH_ENDPOINT)"
	@echo "IMAGE_SERVICE_WAIT_RETRIES=$(IMAGE_SERVICE_WAIT_RETRIES)"
	@echo "IMAGE_SERVICE_WAIT_SECONDS=$(IMAGE_SERVICE_WAIT_SECONDS)"
	@echo "EMBED_MODEL_PATH=$(EMBED_MODEL_PATH)"

require-vps-env:
	@test -n "$(VPS_USER)" || (echo "Missing VPS user. Set VINYL_KEEPER_VPS_USER in .env or shell env." && exit 1)
	@test -n "$(VPS_HOST)" || (echo "Missing VPS IP/host. Set VINYL_KEEPER_VPS_IP in .env or shell env." && exit 1)

check-images:
	@podman image exists $(IMAGE_APP) || (echo "Missing image: $(IMAGE_APP). Run 'make build' first." && exit 1)
	@podman image exists $(IMAGE_SERVICE) || (echo "Missing image: $(IMAGE_SERVICE). Run 'make build' first." && exit 1)

# Build local images (used by both dev and deploy)
build-bin:
	mkdir -p $(APP_BIN_DIR)
	CGO_ENABLED=0 GOOS=$(APP_GOOS) GOARCH=$(APP_GOARCH) go -C app build -o build/vinyl-keeper .

build: build-bin
	podman build -f $(APP_DOCKERFILE) --build-arg APP_BINARY=$(APP_BIN) -t $(IMAGE_APP) .
	podman build -t $(IMAGE_SERVICE) ./image_service

# Generate local dev certs (for camera access on phone)
certs:
	mkdir -p ./certs
	@command -v openssl >/dev/null 2>&1 || { echo "Error: openssl is required"; exit 1; }
	@SAN="DNS:localhost,IP:127.0.0.1"; \
	if [ -n "$(LAN_IP)" ]; then SAN="$$SAN,IP:$(LAN_IP)"; fi; \
	echo "Generating cert with SAN: $$SAN"; \
	openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
		-keyout certs/dev.key \
		-out certs/dev.crt \
		-subj "/CN=localhost" \
		-addext "subjectAltName=$$SAN" >/dev/null; \
	echo "Wrote certs/dev.crt and certs/dev.key (LAN_IP=$(if $(LAN_IP),$(LAN_IP),none))"

# Run local env from prebuilt images with HTTPS (phone-accessible for camera)
dev: check-images certs
	@test -f certs/dev.crt || { echo "Error: certs/dev.crt not found. Run 'make certs' first."; exit 1; }
	mkdir -p ./data
	IMAGE_SERVICE_HEALTH_ENDPOINT=$(IMAGE_SERVICE_HEALTH_ENDPOINT) \
	IMAGE_SERVICE_WAIT_RETRIES=$(IMAGE_SERVICE_WAIT_RETRIES) \
	IMAGE_SERVICE_WAIT_SECONDS=$(IMAGE_SERVICE_WAIT_SECONDS) \
	podman-compose -f $(COMPOSE_LOCAL) up -d --force-recreate --remove-orphans
	@echo "Local app: https://127.0.0.1:8080"
	@echo "Phone/LAN: https://$(if $(LAN_IP),$(LAN_IP),<no LAN IP detected>):8080"
	@echo "Accept the self-signed cert in browser for camera access"

# Native fallback: no containers, runs image-service + go run on host
dev-native: certs
	TLS_CERT=$(TLS_CERT) \
	TLS_KEY=$(TLS_KEY) \
	ENABLE_TLS=$(ENABLE_TLS) \
	IMAGE_SERVICE_HOST=$(IMAGE_SERVICE_HOST) \
	IMAGE_SERVICE_PORT=$(IMAGE_SERVICE_PORT) \
	IMAGE_SERVICE_ENDPOINT=$(IMAGE_SERVICE_ENDPOINT) \
	IMAGE_SERVICE_HEALTH_ENDPOINT=$(IMAGE_SERVICE_HEALTH_ENDPOINT) \
	IMAGE_SERVICE_WAIT_RETRIES=$(IMAGE_SERVICE_WAIT_RETRIES) \
	IMAGE_SERVICE_WAIT_SECONDS=$(IMAGE_SERVICE_WAIT_SECONDS) \
	EMBED_MODEL_PATH=$(EMBED_MODEL_PATH) \
	EMBED_MODEL_FAMILY=$(EMBED_MODEL_FAMILY) \
	EMBED_DIM=$(EMBED_DIM) \
	EMBED_IMAGE_SIZE=$(EMBED_IMAGE_SIZE) \
	./scripts/run-local.sh

dev-down:
	podman-compose -f $(COMPOSE_LOCAL) down --remove-orphans

# Save built images for transport
save: build
	mkdir -p $(DEPLOY_DIR)
	podman save -o $(DEPLOY_APP_TAR) $(IMAGE_APP)
	podman save -o $(DEPLOY_SERVICE_TAR) $(IMAGE_SERVICE)
	cp $(COMPOSE_PROD) $(DEPLOY_DIR)/

# Build, ship, and deploy to VPS
cicd: require-vps-env save
	ssh $(VPS_USER)@$(VPS_HOST) "mkdir -p $(VPS_PROD_DIR)/images"
	scp $(DEPLOY_APP_TAR) $(VPS_USER)@$(VPS_HOST):$(VPS_PROD_DIR)/images/app.tar
	scp $(DEPLOY_SERVICE_TAR) $(VPS_USER)@$(VPS_HOST):$(VPS_PROD_DIR)/images/service.tar
	scp $(COMPOSE_PROD) $(VPS_USER)@$(VPS_HOST):$(VPS_PROD_DIR)/$(COMPOSE_PROD)
	ssh $(VPS_USER)@$(VPS_HOST) "podman load -i $(VPS_PROD_DIR)/images/app.tar && podman load -i $(VPS_PROD_DIR)/images/service.tar && podman-compose -f $(VPS_PROD_DIR)/$(COMPOSE_PROD) up -d --force-recreate --remove-orphans"

clean:
	podman-compose -f $(COMPOSE_LOCAL) down --remove-orphans || true
	rm -rf $(APP_BIN_DIR)
	rm -rf $(DEPLOY_DIR)

.PHONY: help show-config build-bin build check-images certs dev dev-native dev-down save require-vps-env cicd clean
