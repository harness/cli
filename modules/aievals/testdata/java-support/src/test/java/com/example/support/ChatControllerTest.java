package com.example.support;

class ChatControllerTest {
    void chat() {
        System.getenv("SUPPORT_TOKEN");
        mockMvc.perform(post("/api/chat").header("Authorization", System.getenv("SUPPORT_TOKEN")).content("{\"text\":\"hello\"}"))
            .andExpect(jsonPath("$.output.text").value("ok"));
    }
}
