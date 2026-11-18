import os

from fastapi.testclient import TestClient
from support.main import app

client = TestClient(app)

def test_chat():
    response = client.post("/chat", headers={"Authorization": os.environ["SUPPORT_TOKEN"]}, json={"text": "hello"})
    assert response.json()["output"]["text"] == "ok"
