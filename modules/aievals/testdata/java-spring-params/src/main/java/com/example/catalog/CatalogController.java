package com.example.catalog;

import org.springframework.web.bind.annotation.PathVariable;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;
import java.util.UUID;

@RestController
@RequestMapping("/api/catalog")
public class CatalogController {
    @PostMapping("/items/{itemId}/analyze")
    public AnalyzeResponse analyze(
            @PathVariable("itemId") UUID item,
            @RequestParam(name = "includeDetails") Integer details,
            AnalyzeRequest request) {
        return null;
    }
}

record AnalyzeRequest(String query) {}
record AnalyzeResponse(String result) {}
