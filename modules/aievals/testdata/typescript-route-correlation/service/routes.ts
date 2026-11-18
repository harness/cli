import express from "express"

const app = express()

interface ProtectedRequest {
  text: string
}

interface Response {
  text: string
}

app.get("/reports", (req: Request): Response => ({ text: "public" }))
app.post("/reports", (req: ProtectedRequest): Response => ({ text: req.text }))
app.get("/public", (req: Request): Response => ({ text: "public" }))
app.get("/public/admin", (req: Request): Response => ({ text: "admin" }))

export default app
