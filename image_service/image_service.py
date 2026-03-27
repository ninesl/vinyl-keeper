import io
import os

import numpy as np
import onnxruntime as ort
from fastapi import FastAPI, Request, Response
from PIL import Image

# Normalization values per model family.
# https://github.com/openai/CLIP/blob/main/clip/clip.py#L79-L80
# https://pytorch.org/vision/stable/models.html
NORM_VALUES = {
    "clip": {
        "mean": [0.48145466, 0.4578275, 0.40821073],
        "std": [0.26862954, 0.26130258, 0.27577711],
    },
    "imagenet": {
        "mean": [0.485, 0.456, 0.406],
        "std": [0.229, 0.224, 0.225],
    },
}

model_path = os.environ.get("EMBED_MODEL_PATH", "")
model_family = os.environ.get("EMBED_MODEL_FAMILY", "clip")
embed_dim = int(os.environ.get("EMBED_DIM", "512"))
image_size = int(os.environ.get("EMBED_IMAGE_SIZE", "224"))

if model_family not in NORM_VALUES:
    raise ValueError(
        f"unknown EMBED_MODEL_FAMILY: {model_family}, expected one of {list(NORM_VALUES.keys())}"
    )

norm = NORM_VALUES[model_family]

if not model_path:
    raise ValueError("EMBED_MODEL_PATH is required")

session = ort.InferenceSession(model_path)

app = FastAPI(title="vinyl-keeper-image-service")


def preprocess(img_bytes: bytes) -> np.ndarray:
    """Decode raw image bytes, resize, normalize, return [1, 3, H, W] float32 array."""
    img = Image.open(io.BytesIO(img_bytes)).convert("RGB")
    img = img.resize((image_size, image_size), Image.BICUBIC)

    # [H, W, 3] uint8 -> float32 0-1
    arr = np.array(img, dtype=np.float32) / 255.0

    # Normalize per channel
    mean = np.array(norm["mean"], dtype=np.float32)
    std = np.array(norm["std"], dtype=np.float32)
    arr = (arr - mean) / std

    # [H, W, 3] -> [1, 3, H, W]
    arr = np.transpose(arr, (2, 0, 1))
    arr = np.expand_dims(arr, axis=0)

    return arr


@app.get("/health")
def health():
    return Response(status_code=200)


@app.post("/embed")
async def embed(request: Request) -> Response:
    raw = await request.body()
    if len(raw) == 0:
        return Response(content=b"empty image", status_code=400)

    tensor = preprocess(raw)
    result = session.run(["embedding"], {"image": tensor})
    embedding = result[0][0]  # shape (512,) float32

    # Convert to float64 little-endian bytes
    embedding_f64 = embedding.astype(np.float64)

    return Response(
        content=embedding_f64.tobytes(), media_type="application/octet-stream"
    )
