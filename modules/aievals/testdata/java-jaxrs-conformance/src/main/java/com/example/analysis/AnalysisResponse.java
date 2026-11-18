package com.example.analysis;

import java.util.List;

public record AnalysisResponse(
        AnalysisMode mode,
        String reasoning,
        List<AnalysisTheory> theories,
        List<String> toolCalls) {}
