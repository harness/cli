import { ChatRequest, ChatResponse } from "./types";

const app = express();
app.post("/chat", (req: ChatRequest, res: Response<ChatResponse>) => {});
app.listen(3000);
