package com.example.support;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api")
public class ChatController {
    @PostMapping("/chat")
    public ChatResponse chat(@RequestBody ChatRequest body, @RequestHeader("Authorization") String authorization) {
        return null;
    }
}
