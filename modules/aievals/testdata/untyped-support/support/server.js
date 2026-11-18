app.post("/chat", (req, res) => {
  res.json({ output: { text: "ok" } });
});
app.listen(4000);
