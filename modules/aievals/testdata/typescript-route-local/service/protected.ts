import express from "express"

const app = express()

interface ProtectedRequest {
  text: string
}

interface ProtectedResponse {
  result: string
}

app.post("/protected", (req: ProtectedRequest): ProtectedResponse => ({ result: req.text }))
