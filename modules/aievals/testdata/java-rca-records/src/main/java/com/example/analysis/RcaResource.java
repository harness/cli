package com.example.analysis;

import javax.ws.rs.POST;
import javax.ws.rs.Path;
import javax.ws.rs.QueryParam;

@Path("/api/v1/mc/evals/rca")
@HarnessAuth
public class RcaResource {
    @POST
    @Path("/analyze")
    public AnalyzeRcaResponse analyze(
            @QueryParam("accountId") String accountId,
            @QueryParam("orgId") String orgId,
            @QueryParam("projectId") String projectId,
            AnalyzeRcaRequest request) {
        return null;
    }
}
