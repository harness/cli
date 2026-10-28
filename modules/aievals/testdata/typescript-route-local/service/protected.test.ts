const token = process.env.PROTECTED_TOKEN

request(app)
  .post("/protected")
  .set("Authorization", token)
  .send({ text: "protected-input" })
