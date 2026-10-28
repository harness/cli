from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()


class PublicResponse(BaseModel):
    result: str


@app.get("/public")
def public_status() -> PublicResponse:
    return PublicResponse(result="ok")
