FROM ghcr.io/astral-sh/uv:python3.14-trixie

WORKDIR /app

# Copy dependency files first for Docker layer caching
COPY pyproject.toml uv.lock ./

# Install dependencies (cached unless pyproject.toml or uv.lock changes)
RUN uv sync --frozen --no-install-project

# Copy application code
COPY image_service.py ./image_service.py
COPY models ./models

# Install project into the environment
RUN uv sync --frozen

EXPOSE 8000

CMD ["uv", "run", "uvicorn", "image_service:app", "--host", "0.0.0.0", "--port", "8000"]
