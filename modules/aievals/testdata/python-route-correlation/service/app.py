from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()


class ProtectedRequest(BaseModel):
    text: str


class Response(BaseModel):
    text: str


@app.get("/reports")
def reports() -> Response:
    return Response(text="public")


@app.post("/reports")
def protected_reports(request: ProtectedRequest) -> Response:
    return Response(text=request.text)


@app.get("/public")
def public_page() -> Response:
    return Response(text="public")


@app.get("/public/admin")
def admin_page() -> Response:
    return Response(text="admin")
