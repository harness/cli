package com.example.analysis;

import java.util.List;

public record AnalysisRequest(
        AnalysisMode mode,
        String query,
        List<String> validServices,
        List<String> validEnvironments,
        List<String> impactedServices,
        String incidentContext,
        String baseTime,
        List<AnalysisTheory> currentTheories,
        boolean includeChanges,
        boolean includeAlerts,
        boolean includeFlags,
        AnalysisToolResults toolResults) {}
