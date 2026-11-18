from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()


class ProtectedRequest(BaseModel):
    text: str


class ProtectedResponse(BaseModel):
    result: str


@app.post("/protected")
def protected_action(request: ProtectedRequest) -> ProtectedResponse:
    return ProtectedResponse(result=request.text)
