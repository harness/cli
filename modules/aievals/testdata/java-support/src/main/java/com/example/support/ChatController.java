package com.example.support;

@RestController
@RequestMapping("/api")
public class ChatController {
    @PostMapping("/chat")
    public ChatResponse chat(@RequestBody ChatRequest body, @RequestHeader("Authorization") String authorization) {
        return null;
    }
}
