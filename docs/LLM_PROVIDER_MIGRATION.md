# LLM Provider Migration Guide

## Overview

CodeGraph now supports multiple LLM providers through a unified abstraction layer. This guide explains how to use and configure different providers.

**Supported Providers:**
- **Gemini** - Google's Gemini API (default for existing users)
- **LiteLLM** - Unified proxy for 100+ LLM providers (recommended)
- **OpenAI** - Direct OpenAI API integration

---

## Quick Start

### Using LiteLLM (Recommended)

LiteLLM is the recommended provider as it gives you access to 100+ models from different providers through a single interface.

```bash
# 1. Set up LiteLLM proxy (see LiteLLM Setup below)
# 2. Configure CodeGraph
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-1234"
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"

# 3. Run feature linking
./bin/codegraph link features
```

### Using Gemini (Existing Method)

```bash
# Method 1: Environment variables (backward compatible)
export GEMINI_API_KEY="your-gemini-key"
./bin/codegraph link features --gemini

# Method 2: New unified approach
export LLM_PROVIDER="gemini"
export LLM_API_KEY="your-gemini-key"
./bin/codegraph link features
```

### Using OpenAI Directly

```bash
export LLM_PROVIDER="openai"
export LLM_API_KEY="sk-..."
export LLM_BASE_URL="https://api.openai.com/v1"
export LLM_TEXT_MODEL="gpt-4"
export LLM_EMBEDDING_MODEL="text-embedding-3-small"

./bin/codegraph link features
```

---

## Environment Variables

### Core Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `LLM_PROVIDER` | Provider type: `gemini`, `litellm`, `openai` | `litellm` (or `gemini` if `GEMINI_API_KEY` set) | No |
| `LLM_API_KEY` | API key for the provider | - | Yes |
| `LLM_BASE_URL` | Base URL for LiteLLM/OpenAI | - | Yes (for litellm/openai) |

### Model Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `LLM_TEXT_MODEL` | Model for text generation | Provider-dependent |
| `LLM_EMBEDDING_MODEL` | Model for embeddings | Provider-dependent |
| `LLM_TEMPERATURE` | Temperature for generation | `0.2` |
| `LLM_MAX_TOKENS` | Max tokens per request | `1024` |

### Default Models by Provider

| Provider | Text Model | Embedding Model |
|----------|------------|-----------------|
| Gemini | `gemini-1.5-flash` | `gemini-embedding-001` (768-dim) |
| LiteLLM | `openai/gpt-4` | `openai/text-embedding-3-small` |
| OpenAI | `gpt-4` | `text-embedding-3-small` |

### Backward Compatibility

| Old Variable | New Variable | Status |
|--------------|--------------|--------|
| `GEMINI_API_KEY` | `LLM_API_KEY` | ✅ Still supported |

---

## CLI Flags

### New Unified Flags

```bash
./bin/codegraph link features \
  --provider litellm \
  --api-key "sk-1234" \
  --llm-base-url "http://localhost:4000" \
  --text-model "openai/gpt-4" \
  --embedding-model "openai/text-embedding-3-small"
```

### Flag Reference

| Flag | Description | Example |
|------|-------------|---------|
| `--provider` | Provider type | `--provider litellm` |
| `--api-key` | API key | `--api-key sk-1234` |
| `--llm-base-url` | Base URL | `--llm-base-url http://localhost:4000` |
| `--text-model` | Text model | `--text-model openai/gpt-4` |
| `--embedding-model` | Embedding model | `--embedding-model openai/text-embedding-3-small` |

### Deprecated Flags (Still Supported)

| Flag | Replacement | Status |
|------|-------------|--------|
| `--gemini` | `--provider gemini` | ⚠️ Deprecated |
| `--model` | `--embedding-model` | ⚠️ Deprecated |

---

## LiteLLM Setup

### Installation

```bash
# Using pip
pip install litellm[proxy]

# Using Docker
docker pull ghcr.io/berriai/litellm:main-latest
```

### Configuration

Create `litellm_config.yaml`:

```yaml
model_list:
  # OpenAI Models
  - model_name: openai/gpt-4
    litellm_params:
      model: gpt-4
      api_key: os.environ/OPENAI_API_KEY

  - model_name: openai/text-embedding-3-small
    litellm_params:
      model: text-embedding-3-small
      api_key: os.environ/OPENAI_API_KEY

  # Anthropic Models
  - model_name: anthropic/claude-3-sonnet
    litellm_params:
      model: claude-3-sonnet-20240229
      api_key: os.environ/ANTHROPIC_API_KEY

  # Google Models
  - model_name: gemini/gemini-1.5-flash
    litellm_params:
      model: gemini/gemini-1.5-flash
      api_key: os.environ/GEMINI_API_KEY

  # Add more providers as needed...

litellm_settings:
  drop_params: true
  set_verbose: true
```

### Running LiteLLM Proxy

```bash
# Method 1: Direct command
export OPENAI_API_KEY="sk-..."
export ANTHROPIC_API_KEY="sk-ant-..."
litellm --config litellm_config.yaml --port 4000

# Method 2: Docker
docker run -p 4000:4000 \
  -e OPENAI_API_KEY="sk-..." \
  -e ANTHROPIC_API_KEY="sk-ant-..." \
  -v $(pwd)/litellm_config.yaml:/app/config.yaml \
  ghcr.io/berriai/litellm:main-latest \
  --config /app/config.yaml --port 4000
```

### Verify LiteLLM is Running

```bash
curl http://localhost:4000/health
```

---

## Model Name Formats

### LiteLLM Model Names

LiteLLM uses the format `provider/model-name`:

```bash
# OpenAI
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"

# Anthropic
export LLM_TEXT_MODEL="anthropic/claude-3-sonnet"

# Google (via LiteLLM)
export LLM_TEXT_MODEL="gemini/gemini-1.5-flash"

# Mistral
export LLM_TEXT_MODEL="mistral/mistral-large"
```

### Direct Provider Model Names

When using `openai` or `gemini` providers directly:

```bash
# OpenAI (no prefix)
export LLM_TEXT_MODEL="gpt-4"
export LLM_EMBEDDING_MODEL="text-embedding-3-small"

# Gemini (no prefix)
export LLM_TEXT_MODEL="gemini-1.5-flash"
export LLM_EMBEDDING_MODEL="gemini-embedding-001"
```

---

## Migration Checklist

### From Gemini to LiteLLM

- [ ] Set up LiteLLM proxy server
- [ ] Update environment variables:
  ```bash
  # Old
  export GEMINI_API_KEY="..."

  # New
  export LLM_PROVIDER="litellm"
  export LLM_BASE_URL="http://localhost:4000"
  export LLM_API_KEY="sk-1234"
  export LLM_TEXT_MODEL="openai/gpt-4"
  export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"
  ```
- [ ] Update CLI commands:
  ```bash
  # Old
  ./bin/codegraph link features --gemini

  # New
  ./bin/codegraph link features
  ```
- [ ] Test the integration
- [ ] Remove old environment variables (optional)

### From Custom Embedding Service to Unified Provider

- [ ] Choose a provider (litellm, openai, gemini)
- [ ] Set provider-specific environment variables
- [ ] Remove old `--api-key` and `--base-url` patterns
- [ ] Use new `--provider` and `--llm-base-url` flags

---

## Usage Examples

### Example 1: Gemini (Backward Compatible)

```bash
# Still works exactly as before
export GEMINI_API_KEY="your-key"
./bin/codegraph link features --gemini --dry-run
```

### Example 2: LiteLLM with Multiple Models

```bash
# Setup
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-1234"

# Use GPT-4 for text, OpenAI embeddings
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"

./bin/codegraph link features \
  --min-confidence 0.7 \
  --max-candidates 15

# Switch to Claude for text generation
export LLM_TEXT_MODEL="anthropic/claude-3-sonnet"

./bin/codegraph link features
```

### Example 3: OpenAI Direct

```bash
export LLM_PROVIDER="openai"
export LLM_API_KEY="sk-..."
export LLM_BASE_URL="https://api.openai.com/v1"
export LLM_TEXT_MODEL="gpt-4o-mini"
export LLM_EMBEDDING_MODEL="text-embedding-3-small"

./bin/codegraph link features --verbose
```

### Example 4: Mixed Providers (Advanced)

Use LiteLLM to route to different providers based on model:

```bash
export LLM_PROVIDER="litellm"
export LLM_BASE_URL="http://localhost:4000"
export LLM_API_KEY="sk-1234"

# Use Gemini for embeddings (cheap), GPT-4 for text (powerful)
export LLM_TEXT_MODEL="openai/gpt-4"
export LLM_EMBEDDING_MODEL="gemini/gemini-embedding-001"

./bin/codegraph link features
```

---

## Troubleshooting

### Error: "unknown provider type"

**Problem**: Invalid provider name

**Solution**: Use one of: `gemini`, `litellm`, `openai`

```bash
export LLM_PROVIDER="litellm"  # ✅ Correct
export LLM_PROVIDER="gpt4"     # ❌ Wrong
```

### Error: "LiteLLM provider requires base URL"

**Problem**: Missing `LLM_BASE_URL` for LiteLLM/OpenAI

**Solution**:

```bash
export LLM_BASE_URL="http://localhost:4000"
```

### Error: "API key is required"

**Problem**: No API key provided

**Solution**:

```bash
export LLM_API_KEY="your-key"
# or
./bin/codegraph link features --api-key "your-key"
```

### LiteLLM Connection Refused

**Problem**: LiteLLM proxy not running

**Solution**:

```bash
# Start LiteLLM
litellm --config litellm_config.yaml --port 4000

# Verify
curl http://localhost:4000/health
```

### Embedding Dimension Mismatch

**Problem**: Different embedding models have different dimensions

**Solution**: Rebuild Neo4j vector indexes when changing embedding models

```bash
# Drop old indexes
./bin/codegraph schema drop

# Recreate with new dimensions
./bin/codegraph schema create

# Re-index your code
./bin/codegraph index project .
```

---

## Best Practices

### 1. Use LiteLLM for Production

LiteLLM provides:
- Unified interface to 100+ models
- Automatic retries and fallbacks
- Cost tracking
- Rate limiting
- Caching

### 2. Environment Variables Over Flags

Prefer environment variables for credentials:

```bash
# ✅ Good - credentials in env
export LLM_API_KEY="sk-..."
./bin/codegraph link features

# ❌ Avoid - credentials in shell history
./bin/codegraph link features --api-key "sk-..."
```

### 3. Use .env Files

Create `.env` file:

```bash
LLM_PROVIDER=litellm
LLM_BASE_URL=http://localhost:4000
LLM_API_KEY=sk-1234
LLM_TEXT_MODEL=openai/gpt-4
LLM_EMBEDDING_MODEL=openai/text-embedding-3-small
```

Load with:

```bash
set -a
source .env
set +a
```

### 4. Test with Dry Run

Always test provider configuration first:

```bash
./bin/codegraph link features --dry-run
```

---

## Cost Optimization

### Model Selection by Provider

**Budget-Friendly:**
```bash
export LLM_TEXT_MODEL="openai/gpt-3.5-turbo"       # ~$0.50/1M tokens
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-small"  # ~$0.02/1M tokens
```

**Balanced:**
```bash
export LLM_TEXT_MODEL="gemini/gemini-1.5-flash"    # Free tier available
export LLM_EMBEDDING_MODEL="gemini/gemini-embedding-001"    # Free tier available
```

**High-Performance:**
```bash
export LLM_TEXT_MODEL="anthropic/claude-3-opus"    # ~$15/1M tokens
export LLM_EMBEDDING_MODEL="openai/text-embedding-3-large"  # ~$0.13/1M tokens
```

---

## See Also

- [LiteLLM Documentation](https://docs.litellm.ai)
- [OpenAI API Reference](https://platform.openai.com/docs)
- [Google Gemini API](https://ai.google.dev/gemini-api/docs)
- [CodeGraph Architecture](./architecture/)
- [RFC-002: LLM-based Linking](./rfc/002-llm-based-linking.md)
