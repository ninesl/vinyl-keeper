# Usage: make run-local
run-local:
	TLS_CERT=certs/dev.crt \
	TLS_KEY=certs/dev.key \
	IMAGE_SERVICE_HOST=127.0.0.1 \
	IMAGE_SERVICE_PORT=8000 \
	IMAGE_SERVICE_ENDPOINT=/embed \
	EMBED_MODEL_PATH=models/clip-vit-b-32.onnx \
	EMBED_MODEL_FAMILY=clip \
	EMBED_DIM=512 \
	EMBED_IMAGE_SIZE=224 \
	./scripts/run-local.sh
