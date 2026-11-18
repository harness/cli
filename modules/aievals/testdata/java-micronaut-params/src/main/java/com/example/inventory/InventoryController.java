package com.example.inventory;

import io.micronaut.http.annotation.Controller;
import io.micronaut.http.annotation.PathVariable;
import io.micronaut.http.annotation.Post;
import io.micronaut.http.annotation.QueryValue;
import java.util.UUID;

@Controller("/api/inventory")
public class InventoryController {
    @Post("/items/{itemId}")
    public ItemResponse getItem(
            @PathVariable UUID itemId,
            @QueryValue("view") Integer responseView,
            ItemRequest request) {
        return null;
    }
}

record ItemRequest(String query) {}
record ItemResponse(String summary) {}
