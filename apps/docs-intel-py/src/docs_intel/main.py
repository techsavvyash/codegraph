"""Document intelligence sidecar for CodeGraph."""
from __future__ import annotations

import hashlib
import html
import re
from typing import Any

from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

app = FastAPI(title="CodeGraph docs-intel", version="0.1.0")


# ── Request / Response schemas ──────────────────────────────────────────────

class ParseRequest(BaseModel):
    source: str          # "html" | "confluence" | "text"
    content: str         # raw document content
    node_key_prefix: str = ""  # optional prefix for generated nodeKeys


class Chunk(BaseModel):
    chunk_id: str
    text: str
    position: int
    word_count: int


class ParseResponse(BaseModel):
    node_key: str
    title: str
    plain_text: str
    chunks: list[Chunk]
    metadata: dict[str, Any]


# ── Helpers ──────────────────────────────────────────────────────────────────

def _html_to_text(html_content: str) -> tuple[str, str]:
    """Extract title and plain text from HTML. Returns (title, plain_text)."""
    try:
        from bs4 import BeautifulSoup
        soup = BeautifulSoup(html_content, "html.parser")
        title = soup.find("title")
        title_text = title.get_text(strip=True) if title else ""
        # Remove script/style elements
        for tag in soup(["script", "style", "nav", "footer"]):
            tag.decompose()
        plain = soup.get_text(separator=" ", strip=True)
        plain = re.sub(r"\s+", " ", plain).strip()
        return title_text, plain
    except ImportError:
        # Fallback: strip HTML tags with regex
        title_match = re.search(r"<title[^>]*>(.*?)</title>", html_content, re.IGNORECASE | re.DOTALL)
        title_text = title_match.group(1).strip() if title_match else ""
        plain = re.sub(r"<[^>]+>", " ", html_content)
        plain = html.unescape(plain)
        plain = re.sub(r"\s+", " ", plain).strip()
        return title_text, plain


def _chunk_text(text: str, max_words: int = 200, overlap: int = 20) -> list[Chunk]:
    """Split text into overlapping chunks."""
    words = text.split()
    chunks: list[Chunk] = []
    i = 0
    position = 0
    while i < len(words):
        chunk_words = words[i:i + max_words]
        chunk_text = " ".join(chunk_words)
        chunk_id = hashlib.sha256(chunk_text.encode()).hexdigest()[:16]
        chunks.append(Chunk(
            chunk_id=chunk_id,
            text=chunk_text,
            position=position,
            word_count=len(chunk_words),
        ))
        i += max_words - overlap
        position += 1
        if len(chunk_words) < max_words:
            break
    return chunks


def _make_node_key(prefix: str, content: str) -> str:
    digest = hashlib.sha256(content.encode()).hexdigest()[:24]
    return f"{prefix}:{digest}" if prefix else f"doc:{digest}"


# ── Endpoints ────────────────────────────────────────────────────────────────

@app.get("/health")
def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/parse", response_model=ParseResponse)
def parse(req: ParseRequest) -> ParseResponse:
    source = req.source.lower()

    if source in ("html", "confluence"):
        title, plain_text = _html_to_text(req.content)
    elif source == "text":
        title = ""
        plain_text = req.content.strip()
    else:
        raise HTTPException(status_code=400, detail=f"Unknown source type: {req.source!r}")

    if not plain_text:
        raise HTTPException(status_code=422, detail="Extracted text is empty")

    node_key = _make_node_key(req.node_key_prefix, plain_text)
    chunks = _chunk_text(plain_text)
    metadata: dict[str, Any] = {
        "source": source,
        "char_count": len(plain_text),
        "word_count": len(plain_text.split()),
        "chunk_count": len(chunks),
    }
    if title:
        metadata["title"] = title

    return ParseResponse(
        node_key=node_key,
        title=title,
        plain_text=plain_text,
        chunks=chunks,
        metadata=metadata,
    )


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8765)
