FROM python:3.11-slim

WORKDIR /app

# Install uv for fast dependency installation
RUN pip install --no-cache-dir uv

# Copy dependency manifests first for layer caching
COPY pyproject.toml ./

# Install production dependencies only
RUN uv pip install --system --no-cache ".[standard]" || \
    uv pip install --system --no-cache fastapi "uvicorn[standard]" beautifulsoup4 html2text

# Copy source
COPY src/ ./src/

ENV PORT=8765
EXPOSE 8765

CMD ["python", "-m", "uvicorn", "docs_intel.main:app", "--host", "0.0.0.0", "--port", "8765"]
