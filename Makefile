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

VPS_HOST ?=
PROD_DOMAIN ?=
VPS_USER ?= debian
VPS_HOME ?= /home/$(VPS_USER)
VPS_PROD_DIR ?= $(VPS_HOME)/prod/vinylkeeper
VPS_IMAGES_DIR ?= $(VPS_PROD_DIR)/images
VPS_DATA_DIR ?= $(VPS_PROD_DIR)/data
VPS_MODELS_DIR ?= $(VPS_PROD_DIR)/models
VPS_APP_TAR_PATH ?= $(VPS_IMAGES_DIR)/app.tar
VPS_COMPOSE_PATH ?= $(VPS_PROD_DIR)/$(COMPOSE_PROD)
VPS_ENV_PATH ?= $(VPS_PROD_DIR)/.env
NGINX_LOCAL_CONF ?= nginx/vinylkeeper.conf
VPS_NGINX_DIR ?= $(VPS_HOME)/prod/nginx
VPS_NGINX_PATH ?= $(VPS_NGINX_DIR)/vinylkeeper.conf
VPS_NGINX_ACTIVE_PATH ?= /etc/nginx/conf.d/vinylkeeper.conf
MODEL_SYNC_SOURCE_DIR ?= image_service/models
SERVICE_BUILD_SOURCE_DIR ?= image_service
VPS_SERVICE_BUILD_DIR ?= $(VPS_PROD_DIR)/image_service_build
DETECTED_LAN_IP := $(shell ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.*src \([0-9.]*\).*/\1/p' | head -n 1)
LAN_IP_IN_USE := $(if $(LAN_IP),$(LAN_IP),$(DETECTED_LAN_IP))

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
EMBED_MODEL_PATH_DEV ?=
EMBED_MODEL_PATH_PROD ?=
EMBED_MODEL_FAMILY ?=
EMBED_DIM ?=
EMBED_IMAGE_SIZE ?=
DB_PATH ?= $(CURDIR)/data/vinylkeeper.db
APP_PORT ?= 8080

COMPOSE_LOCAL = podman-compose.local.yml
COMPOSE_PROD = podman-compose.prod.yml

DEPLOY_DIR = deploy
DEPLOY_APP_TAR = $(DEPLOY_DIR)/app.tar
DEPLOY_COMPOSE = $(DEPLOY_DIR)/$(COMPOSE_PROD)
DEPLOY_ENV = $(DEPLOY_DIR)/.env

help:
	@echo "Public commands:"
	@echo "  make help         Show this help"
	@echo "  make build        Build podman images (Go app + image-service)"
	@echo "  make dev          Run native local HTTPS stack (no containers)"
	@echo "  make dev-pods     Run containerized local HTTPS stack"
	@echo "  make dev-down     Stop local containerized stack"
	@echo "  make cicd         Build, ship, and deploy to VPS"
	@echo "  make deploy-down  Stop prod stack on VPS"
	@echo "  make refresh-prod-nginx Upload nginx config and reload nginx on VPS"
	@echo "  make show-config  Show resolved .env/network values"
	@echo "  make clean        Remove local build/deploy artifacts and stop local stack"
	@echo ""
	@echo "Internal commands (normally called by public commands):"
	@echo "  make templ              Regenerate templ Go files in app/; used by: build, dev, dev-pods"
	@echo "  make templ-watch        Watch templ sources and regenerate Go files"
	@echo "  make tailwind           Generate Tailwind output.css + templ sources; used by: build, dev, dev-pods"
	@echo "  make tailwind-watch     Watch Tailwind changes locally"
	@echo "  make build-bin          Build Go binary from app/ ($(APP_BIN)); used by: build"
	@echo "  make build-service      Build image-service image; used by: build"
	@echo "  make check-images       Verify required images exist; used by: dev-pods"
	@echo "  make certs              Generate local self-signed TLS certs; used by: dev, dev-pods"
	@echo "  make print-dev-urls     Print local/LAN app URLs; used by: dev, dev-pods"
	@echo "  make require-vps-env    Validate VPS env vars; used by: cicd"
	@echo "  make sync-models        Copy model files to $(VPS_MODELS_DIR); used by: cicd"
	@echo "  make sync-service-build Copy image_service build context to $(VPS_SERVICE_BUILD_DIR); used by: cicd"
	@echo "  make clean-deploy       Remove deploy tarballs/files; used by: save"
	@echo "  make save               Build and export app image tarball to $(DEPLOY_DIR); used by: cicd"
	@echo "  make deploy-up          Copy deploy artifacts to VPS and run prod stack; used by: cicd"
	@echo ""
	@echo "See .env/.env.example for all configurable variables"

show-config:
	@echo "VPS_HOST=$(if $(VPS_HOST),$(VPS_HOST),<unset>)"
	@echo "VPS_USER=$(VPS_USER)"
	@echo "VPS_HOME=$(VPS_HOME)"
	@echo "PROD_DOMAIN=$(if $(PROD_DOMAIN),$(PROD_DOMAIN),<unset>)"
	@echo "MODEL_SYNC_SOURCE_DIR=$(MODEL_SYNC_SOURCE_DIR)"
	@echo "SERVICE_BUILD_SOURCE_DIR=$(SERVICE_BUILD_SOURCE_DIR)"
	@echo "VPS_PROD_DIR=$(VPS_PROD_DIR)"
	@echo "VPS_NGINX_DIR=$(VPS_NGINX_DIR)"
	@echo "VPS_IMAGES_DIR=$(VPS_IMAGES_DIR)"
	@echo "VPS_DATA_DIR=$(VPS_DATA_DIR)"
	@echo "VPS_MODELS_DIR=$(VPS_MODELS_DIR)"
	@echo "LAN_IP(override)=$(if $(LAN_IP),$(LAN_IP),<unset>)"
	@echo "DETECTED_LAN_IP=$(if $(DETECTED_LAN_IP),$(DETECTED_LAN_IP),<none>)"
	@echo "LAN_IP(in use)=$(if $(LAN_IP_IN_USE),$(LAN_IP_IN_USE),<none>)"
	@echo "CERT_DIR=$(CERT_DIR)"
	@echo "TLS_CERT=$(TLS_CERT)"
	@echo "TLS_KEY=$(TLS_KEY)"
	@echo "IMAGE_SERVICE_HEALTH_ENDPOINT=$(IMAGE_SERVICE_HEALTH_ENDPOINT)"
	@echo "IMAGE_SERVICE_WAIT_RETRIES=$(IMAGE_SERVICE_WAIT_RETRIES)"
	@echo "IMAGE_SERVICE_WAIT_SECONDS=$(IMAGE_SERVICE_WAIT_SECONDS)"
	@echo "EMBED_MODEL_PATH_DEV=$(EMBED_MODEL_PATH_DEV)"
	@echo "EMBED_MODEL_PATH_PROD=$(EMBED_MODEL_PATH_PROD)"
	@echo "DB_PATH=$(DB_PATH)"

require-vps-env:
	@test -n "$(VPS_HOST)" || (echo "Missing deploy host. Set VPS_HOST in .env or shell env." && exit 1)
	@test -f .env || (echo "Missing .env in repo root. Create it before running make cicd." && exit 1)

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

build-service:
	podman build -t $(IMAGE_SERVICE) ./image_service

build: templ tailwind certs build-bin build-service
	podman build -f $(APP_DOCKERFILE) --build-arg APP_BINARY=$(APP_BIN) -t $(IMAGE_APP) .

# Generate local dev certs (for camera access on phone)
certs:
	mkdir -p ./$(CERT_DIR)
	@command -v openssl >/dev/null 2>&1 || { echo "Error: openssl is required"; exit 1; }
	@SAN="DNS:localhost,IP:127.0.0.1"; \
	if [ -n "$(LAN_IP_IN_USE)" ]; then SAN="$$SAN,IP:$(LAN_IP_IN_USE)"; fi; \
	echo "Generating cert with SAN: $$SAN"; \
	openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
		-keyout "$(TLS_KEY)" \
		-out "$(TLS_CERT)" \
		-subj "/CN=localhost" \
		-addext "subjectAltName=$$SAN" >/dev/null; \
	echo "Wrote $(TLS_CERT) and $(TLS_KEY) (LAN_IP=$(if $(LAN_IP_IN_USE),$(LAN_IP_IN_USE),none))"

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
	EMBED_MODEL_PATH=$(EMBED_MODEL_PATH_DEV) \
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
	echo "Phone/LAN: $$scheme://$(if $(LAN_IP_IN_USE),$(LAN_IP_IN_USE),<no LAN IP detected>):$(APP_PORT)"

dev-down:
	podman-compose -f $(COMPOSE_LOCAL) down --remove-orphans

# Save built images for transport
clean-deploy:
	rm -f $(DEPLOY_APP_TAR) $(DEPLOY_COMPOSE) $(DEPLOY_ENV)

save: templ tailwind certs build-bin clean-deploy
	mkdir -p $(DEPLOY_DIR)
	podman build -f $(APP_DOCKERFILE) --build-arg APP_BINARY=$(APP_BIN) -t $(IMAGE_APP) .
	podman save -o $(DEPLOY_APP_TAR) $(IMAGE_APP)
	cp $(COMPOSE_PROD) $(DEPLOY_COMPOSE)
	python3 scripts/build_deploy_env.py .env $(DEPLOY_ENV)

# Ship artifacts and run prod stack on VPS
deploy-up: require-vps-env save sync-models sync-service-build
	ssh $(VPS_HOST) "mkdir -p $(VPS_IMAGES_DIR) $(VPS_DATA_DIR) $(VPS_MODELS_DIR)"
	@command -v rsync >/dev/null 2>&1 || { echo "Error: rsync is required locally for deploy-up"; exit 1; }
	rsync -avz $(DEPLOY_APP_TAR) $(VPS_HOST):$(VPS_APP_TAR_PATH)
	rsync -avz $(DEPLOY_COMPOSE) $(VPS_HOST):$(VPS_COMPOSE_PATH)
	rsync -avz $(DEPLOY_ENV) $(VPS_HOST):$(VPS_ENV_PATH)
	ssh $(VPS_HOST) 'cd $(VPS_PROD_DIR) && podman load -i $(VPS_APP_TAR_PATH) && podman build -t $(IMAGE_SERVICE) image_service_build && podman-compose -f $(COMPOSE_PROD) up -d --force-recreate --remove-orphans'

# Copy model files to production models directory
sync-models: require-vps-env
	@test -d "$(MODEL_SYNC_SOURCE_DIR)" || (echo "Missing $(MODEL_SYNC_SOURCE_DIR)." && exit 1)
	ssh $(VPS_HOST) "mkdir -p $(VPS_MODELS_DIR)"
	@command -v rsync >/dev/null 2>&1 || { echo "Error: rsync is required locally for sync-models"; exit 1; }
	rsync -avz "$(MODEL_SYNC_SOURCE_DIR)/" $(VPS_HOST):$(VPS_MODELS_DIR)/

# Copy image-service build context to VPS for on-host builds
sync-service-build: require-vps-env
	@test -d "$(SERVICE_BUILD_SOURCE_DIR)" || (echo "Missing $(SERVICE_BUILD_SOURCE_DIR)." && exit 1)
	@command -v rsync >/dev/null 2>&1 || { echo "Error: rsync is required locally for sync-service-build"; exit 1; }
	ssh $(VPS_HOST) "mkdir -p $(VPS_SERVICE_BUILD_DIR)"
	rsync -avz --delete --exclude ".venv/" --exclude "__pycache__/" --exclude "models/" "$(SERVICE_BUILD_SOURCE_DIR)/" $(VPS_HOST):$(VPS_SERVICE_BUILD_DIR)/

# Stop prod stack on VPS
deploy-down: require-vps-env
	ssh $(VPS_HOST) "cd $(VPS_PROD_DIR) && podman-compose -f $(COMPOSE_PROD) down --remove-orphans"

# Upload/activate nginx config for production host proxying
refresh-prod-nginx: require-vps-env
	@test -f "$(NGINX_LOCAL_CONF)" || (echo "Missing $(NGINX_LOCAL_CONF)." && exit 1)
	@test -n "$(PROD_DOMAIN)" || (echo "Missing PROD_DOMAIN in .env (e.g., PROD_DOMAIN=ninescoding.com)" && exit 1)
	@command -v rsync >/dev/null 2>&1 || { echo "Error: rsync is required locally for refresh-prod-nginx"; exit 1; }
	ssh $(VPS_HOST) "mkdir -p $(VPS_NGINX_DIR)"
	rsync -avz "$(NGINX_LOCAL_CONF)" $(VPS_HOST):$(VPS_NGINX_PATH)
	ssh $(VPS_HOST) "sudo ln -sf $(VPS_NGINX_PATH) $(VPS_NGINX_ACTIVE_PATH) && sudo nginx -t && sudo systemctl reload nginx"
	@echo "Verifying HTTPS endpoint..."
	@curl -sSf -o /dev/null -w "HTTP %{http_code} from https://$(PROD_DOMAIN)\n" https://$(PROD_DOMAIN) || \
		(echo "HTTPS check failed for $(PROD_DOMAIN). Check nginx/cert/containers." && exit 1)

# Build, ship, and deploy to VPS
cicd: deploy-up

clean:
	podman-compose -f $(COMPOSE_LOCAL) down --remove-orphans || true
	rm -rf $(APP_BIN_DIR)
	rm -rf $(DEPLOY_DIR)
	rm -rf $(CERT_DIR)

.PHONY: help show-config templ templ-generate templ-watch tailwind tailwind-watch build-bin build-service build check-images certs dev dev-native dev-pods print-dev-urls dev-down clean-deploy save sync-models sync-service-build deploy-up deploy-down refresh-prod-nginx require-vps-env cicd clean
