import { ChatRequest, ChatResponse } from "./types";
import express from "express";

const app = express();
app.post("/chat", (req: ChatRequest, res: Response<ChatResponse>) => {});
app.listen(3000);

export default app;
