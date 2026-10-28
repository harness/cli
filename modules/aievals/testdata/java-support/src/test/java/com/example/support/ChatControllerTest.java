package com.example.support;

import org.springframework.test.web.servlet.MockMvc;
import static org.springframework.test.web.servlet.request.MockMvcRequestBuilders.*;

class ChatControllerTest {
    MockMvc mockMvc;

    void chat() {
        System.getenv("SUPPORT_TOKEN");
        mockMvc.perform(post("/api/chat").header("Authorization", System.getenv("SUPPORT_TOKEN")).content("{\"text\":\"hello\"}"))
            .andExpect(jsonPath("$.output.text").value("ok"));
    }
}
