const body = JSON.stringify({ text: "hello" });
fetch("/chat", { method: "POST", headers: { Authorization: "literal-secret" }, body });
assert.equal(json.output.text, "ok");
