import os


def test_chat(client):
    token = os.environ["SUPPORT_TOKEN"]
    response = client.post("/chat", headers={"Authorization": token}, json={"text": "hello"})
    assert response.json()["output"]["text"] == "ok"
