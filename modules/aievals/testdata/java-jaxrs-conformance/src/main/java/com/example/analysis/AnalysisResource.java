package com.example.analysis;

import com.example.auth.HarnessAuth;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.QueryParam;
import java.util.List;

@Path("/api/v1/analysis")
public class AnalysisResource {
    @POST
    @Path("/analyze")
    @HarnessAuth
    public AnalysisResponse analyze(
            @QueryParam("accountId") String accountId,
            @QueryParam("orgId") String orgId,
            @QueryParam("projectId") String projectId,
            AnalysisRequest request) {
        return null;
    }
}
