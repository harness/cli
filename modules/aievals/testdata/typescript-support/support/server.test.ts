request(app).post("/chat").set("Authorization", process.env.SUPPORT_TOKEN).send({ text: "hello" }).expect({ output: { text: "ok" } });
