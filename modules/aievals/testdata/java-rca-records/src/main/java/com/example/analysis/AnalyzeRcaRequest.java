package com.example.analysis;

import java.util.List;

public record AnalyzeRcaRequest(
        String mode,
        String query,
        String scope,
        String context,
        List<ToolResult> toolResults) {}
