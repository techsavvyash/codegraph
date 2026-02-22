"""Tests for the parse endpoint."""
from fastapi.testclient import TestClient
from docs_intel.main import app

client = TestClient(app)


def test_health():
    resp = client.get("/health")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"


def test_parse_text():
    resp = client.post("/parse", json={
        "source": "text",
        "content": "Hello world. This is a test document with some content.",
    })
    assert resp.status_code == 200
    data = resp.json()
    assert data["node_key"].startswith("doc:")
    assert "hello world" in data["plain_text"].lower()
    assert len(data["chunks"]) >= 1
    assert data["chunks"][0]["word_count"] > 0


def test_parse_html():
    resp = client.post("/parse", json={
        "source": "html",
        "content": "<html><head><title>Test Page</title></head><body><h1>Hello</h1><p>World content here.</p></body></html>",
    })
    assert resp.status_code == 200
    data = resp.json()
    assert data["node_key"].startswith("doc:")
    assert len(data["chunks"]) >= 1


def test_parse_unknown_source():
    resp = client.post("/parse", json={"source": "pdf", "content": "x"})
    assert resp.status_code == 400


def test_node_key_deterministic():
    payload = {"source": "text", "content": "Same content every time."}
    r1 = client.post("/parse", json=payload)
    r2 = client.post("/parse", json=payload)
    assert r1.json()["node_key"] == r2.json()["node_key"]


def test_node_key_prefix():
    resp = client.post("/parse", json={
        "source": "text",
        "content": "Content for prefix test.",
        "node_key_prefix": "confluence",
    })
    assert resp.json()["node_key"].startswith("confluence:")
