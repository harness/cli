import os

from fastapi.testclient import TestClient
from service.app import app

client = TestClient(app)

def test_public_reports():
    response = client.get("/reports")
    assert response.json()["text"] == "public-result"


def test_protected_reports():
    response = client.post("/reports", headers={"Authorization": os.environ["PROTECTED_TOKEN"]}, json={"text": "protected-input"})
    assert response.json()["text"] == "protected-result"


def test_public_page():
    response = client.get("/public")
    assert response.json()["text"] == "public-page-result"


def test_admin_page():
    response = client.get("/public/admin")
    assert response.json()["text"] == "admin-result"
