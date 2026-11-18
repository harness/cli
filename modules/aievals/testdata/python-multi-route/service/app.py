from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI()


class AnalyzeRequest(BaseModel):
    query: str


class AnalyzeResponse(BaseModel):
    result: str


@app.get("/reports")
def get_reports() -> AnalyzeResponse:
    return AnalyzeResponse(result="ok")


@app.post("/reports")
def analyze_reports(request: AnalyzeRequest) -> AnalyzeResponse:
    return AnalyzeResponse(result=request.query)
