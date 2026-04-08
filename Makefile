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
CERT_DIR ?= certs
TLS_CERT_FILE ?= dev.crt
TLS_KEY_FILE ?= dev.key
TLS_CERT ?= $(CERT_DIR)/$(TLS_CERT_FILE)
TLS_KEY ?= $(CERT_DIR)/$(TLS_KEY_FILE)
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
DB_PATH ?= $(CURDIR)/data/vinylkeeper.db
APP_PORT ?= 8080

COMPOSE_LOCAL = podman-compose.local.yml
COMPOSE_PROD = podman-compose.prod.yml

DEPLOY_DIR = deploy
DEPLOY_APP_TAR = $(DEPLOY_DIR)/app.tar
DEPLOY_SERVICE_TAR = $(DEPLOY_DIR)/service.tar

help:
	@echo "Public commands:"
	@echo "  make help        Show this help"
	@echo "  make build       Build podman images (builds Go app from app/)"
	@echo "  make dev         Run native local HTTPS stack (no containers, includes dev-down, templ, certs)"
	@echo "  make dev-pods    Run containerized local HTTPS stack (includes check-images, certs)"
	@echo "  make dev-down    Stop local containerized stack"
	@echo "  make cicd        Build, ship tarballs, and deploy to VPS (includes require-vps-env, save)"
	@echo "  make show-config Show resolved .env/network values"
	@echo ""
	@echo "Internal commands (normally called by public commands):"
	@echo "  make templ           Regenerate templ Go files in app/; used by: build, dev, dev-pods"
	@echo "  make templ-watch     Watch templ sources and regenerate Go files"
	@echo "  make tailwind        Generate Tailwind output.css + templ sources; used by: build, dev, dev-pods"
	@echo "  make tailwind-watch  Watch Tailwind changes locally"
	@echo "  make build-bin       Build Go binary from app/ ($(APP_BIN)); used by: build"
	@echo "  make check-images    Verify required images exist; used by: dev-pods"
	@echo "  make certs           Generate local self-signed TLS certs; used by: dev, dev-pods"
	@echo "  make print-dev-urls  Print local/LAN app URLs; used by: dev, dev-pods"
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
	@echo "CERT_DIR=$(CERT_DIR)"
	@echo "TLS_CERT=$(TLS_CERT)"
	@echo "TLS_KEY=$(TLS_KEY)"
	@echo "IMAGE_SERVICE_HEALTH_ENDPOINT=$(IMAGE_SERVICE_HEALTH_ENDPOINT)"
	@echo "IMAGE_SERVICE_WAIT_RETRIES=$(IMAGE_SERVICE_WAIT_RETRIES)"
	@echo "IMAGE_SERVICE_WAIT_SECONDS=$(IMAGE_SERVICE_WAIT_SECONDS)"
	@echo "EMBED_MODEL_PATH=$(EMBED_MODEL_PATH)"
	@echo "DB_PATH=$(DB_PATH)"

require-vps-env:
	@test -n "$(VPS_USER)" || (echo "Missing VPS user. Set VINYL_KEEPER_VPS_USER in .env or shell env." && exit 1)
	@test -n "$(VPS_HOST)" || (echo "Missing VPS IP/host. Set VINYL_KEEPER_VPS_IP in .env or shell env." && exit 1)

check-images:
	@podman image exists $(IMAGE_APP) || (echo "Missing image: $(IMAGE_APP). Run 'make build' first." && exit 1)
	@podman image exists $(IMAGE_SERVICE) || (echo "Missing image: $(IMAGE_SERVICE). Run 'make build' first." && exit 1)

# Build local images (used by both dev and deploy)
templ:
	@command -v templ >/dev/null 2>&1 || { echo "Error: templ CLI is required (go install github.com/a-h/templ/cmd/templ@latest)"; exit 1; }
	cd app/ && templ generate

templ-generate: templ

templ-watch:
	@command -v templ >/dev/null 2>&1 || { echo "Error: templ CLI is required (go install github.com/a-h/templ/cmd/templ@latest)"; exit 1; }
	cd app/ && templ generate --watch

tailwind:
	@command -v tailwindcss >/dev/null 2>&1 || { echo "Error: tailwindcss CLI is required"; exit 1; }
	mkdir -p app/router/assets/fonts
	cd app/router && \
		TEMPLUI_PATH="$$(go list -mod=mod -m -f '{{.Dir}}' github.com/templui/templui)" && \
		{ echo '@source "./**/*.templ";'; \
		  echo "@source \"$$TEMPLUI_PATH/components/**/*.templ\";"; \
		} > ./assets/css/sources.generated.css && \
		tailwindcss -i ./assets/css/input.css -o ./assets/css/output.css

tailwind-watch:
	@command -v tailwindcss >/dev/null 2>&1 || { echo "Error: tailwindcss CLI is required"; exit 1; }
	mkdir -p app/router/assets/fonts
	cd app/router && \
		TEMPLUI_PATH="$$(go list -mod=mod -m -f '{{.Dir}}' github.com/templui/templui)" && \
		{ echo '@source "./**/*.templ";'; \
		  echo "@source \"$$TEMPLUI_PATH/components/**/*.templ\";"; \
		} > ./assets/css/sources.generated.css && \
		tailwindcss -i ./assets/css/input.css -o ./assets/css/output.css --watch

build-bin: templ-generate tailwind
	mkdir -p $(APP_BIN_DIR)
	CGO_ENABLED=0 GOOS=$(APP_GOOS) GOARCH=$(APP_GOARCH) go -C app build -o build/vinyl-keeper .

build: templ tailwind certs build-bin
	podman build -f $(APP_DOCKERFILE) --build-arg APP_BINARY=$(APP_BIN) -t $(IMAGE_APP) .
	podman build -t $(IMAGE_SERVICE) ./image_service

# Generate local dev certs (for camera access on phone)
certs:
	mkdir -p ./$(CERT_DIR)
	@command -v openssl >/dev/null 2>&1 || { echo "Error: openssl is required"; exit 1; }
	@SAN="DNS:localhost,IP:127.0.0.1"; \
	if [ -n "$(LAN_IP)" ]; then SAN="$$SAN,IP:$(LAN_IP)"; fi; \
	echo "Generating cert with SAN: $$SAN"; \
	openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
		-keyout "$(TLS_KEY)" \
		-out "$(TLS_CERT)" \
		-subj "/CN=localhost" \
		-addext "subjectAltName=$$SAN" >/dev/null; \
	echo "Wrote $(TLS_CERT) and $(TLS_KEY) (LAN_IP=$(if $(LAN_IP),$(LAN_IP),none))"

# Run local env from prebuilt images with HTTPS (phone-accessible for camera)
dev-pods: templ tailwind check-images certs
	@test -f "$(TLS_CERT)" || { echo "Error: $(TLS_CERT) not found. Run 'make certs' first."; exit 1; }
	mkdir -p ./data
	IMAGE_SERVICE_HEALTH_ENDPOINT=$(IMAGE_SERVICE_HEALTH_ENDPOINT) \
	IMAGE_SERVICE_WAIT_RETRIES=$(IMAGE_SERVICE_WAIT_RETRIES) \
	IMAGE_SERVICE_WAIT_SECONDS=$(IMAGE_SERVICE_WAIT_SECONDS) \
	podman-compose -f $(COMPOSE_LOCAL) up -d --force-recreate --remove-orphans
	@$(MAKE) --no-print-directory print-dev-urls
	@if [ "$(ENABLE_TLS)" = "true" ]; then echo "Accept the self-signed cert in browser for camera access"; fi

# Native: no containers, runs image-service + templ/tailwind watch + air on host
dev: dev-down templ tailwind certs
	@command -v air >/dev/null 2>&1 || { echo "Error: air is required (go install github.com/air-verse/air@latest)"; exit 1; }
	@$(MAKE) --no-print-directory print-dev-urls
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
	DB_PATH=$(DB_PATH) \
	./scripts/run-local.sh

# Backward-compatible alias.
dev-native: dev

print-dev-urls:
	@scheme=http; \
	case "$(ENABLE_TLS)" in true|TRUE|True|1|yes|YES|on|ON) scheme=https ;; esac; \
	echo "Local app: $$scheme://127.0.0.1:$(APP_PORT)"; \
	echo "Phone/LAN: $$scheme://$(if $(LAN_IP),$(LAN_IP),<no LAN IP detected>):$(APP_PORT)"

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
	rm -rf $(CERT_DIR)

.PHONY: help show-config templ templ-generate templ-watch tailwind tailwind-watch build-bin build check-images certs dev dev-native dev-pods print-dev-urls dev-down save require-vps-env cicd clean
