from pydantic import BaseModel
import uvicorn


class Output(BaseModel):
    text: str


class ChatResponse(BaseModel):
    output: Output


class ChatRequest(BaseModel):
    text: str


@app.post("/chat")
def chat(body: ChatRequest, authorization: Annotated[str, Header()]) -> ChatResponse:
    return ChatResponse(output=Output(text="ok"))


if __name__ == "__main__":
    uvicorn.run(app, host="127.0.0.1", port=8000)
