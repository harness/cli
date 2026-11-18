package com.example.routes;

import org.springframework.test.web.servlet.MockMvc;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

class RouteControllerTest {
    MockMvc mockMvc;

    void publicReports() {
        mockMvc.perform(get("/reports"))
            .andExpect(jsonPath("$.text").value("public-result"));
    }

    void protectedReports() {
        mockMvc.perform(post("/reports").header("Authorization", System.getenv("PROTECTED_TOKEN")).content("{\"text\":\"protected-input\"}"))
            .andExpect(jsonPath("$.text").value("protected-result"));
    }

    void publicPage() {
        mockMvc.perform(get("/public"))
            .andExpect(jsonPath("$.text").value("public-page-result"));
    }

    void adminPage() {
        mockMvc.perform(get("/public/admin"))
            .andExpect(jsonPath("$.text").value("admin-result"));
    }
}
