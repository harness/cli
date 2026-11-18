import express from "express"

const app = express()

interface PublicResponse {
  result: string
}

app.get("/public", (req: Request): PublicResponse => ({ result: "ok" }))
