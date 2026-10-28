package com.example.work;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/work")
public class WorkController {
    @PostMapping("/analyze")
    public AnalyzeResponse analyze(AnalyzeRequest request) {
        return null;
    }

    @GetMapping("/health")
    public String health() {
        return "ok";
    }
}

record AnalyzeRequest(String query) {}
record AnalyzeResponse(String result) {}
