export interface ChatRequest {
  text: string;
}

export interface ChatResponse {
  output: { text: string };
}
