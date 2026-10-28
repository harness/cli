package com.example.routes;

import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestHeader;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class RouteController {
    @GetMapping("/reports")
    public PublicResponse reports() {
        return null;
    }

    @PostMapping("/reports")
    public ProtectedResponse protectedReports(
            @RequestHeader("Authorization") String authorization,
            @RequestBody ProtectedRequest request) {
        return null;
    }

    @GetMapping("/public")
    public PublicResponse publicPage() {
        return null;
    }

    @GetMapping("/public/admin")
    public PublicResponse adminPage() {
        return null;
    }
}

record ProtectedRequest(String text) {}
record PublicResponse(String text) {}
record ProtectedResponse(String text) {}
