import request from "supertest"
import app from "./routes"

test("public reports", () => {
  request(app)
    .get("/reports")
    .expect({ text: "public-result" })
})

test("protected reports", () => {
  const token = process.env.PROTECTED_TOKEN
  request(app)
    .post("/reports")
    .set("Authorization", token)
    .send({ text: "protected-input" })
    .expect({ text: "protected-result" })
})

test("public page", () => {
  request(app)
    .get("/public")
    .expect({ text: "public-page-result" })
})

test("admin page", () => {
  request(app)
    .get("/public/admin")
    .expect({ text: "admin-result" })
})
