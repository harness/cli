import os


def test_protected_client():
    token = os.environ["PROTECTED_TOKEN"]
    client.post("/protected", headers={"Authorization": token}, json={"text": "protected-input"})
