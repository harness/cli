package com.example.reports;

import javax.ws.rs.POST;
import javax.ws.rs.Path;
import javax.ws.rs.PathParam;
import javax.ws.rs.QueryParam;
import java.util.UUID;

@Path("/api/reports")
public class ReportResource {
    @POST
    @Path("/{reportId}")
    public ReportResponse getReport(
            @PathParam("reportId") UUID id,
            @QueryParam("include") Integer expanded,
            ReportRequest request) {
        return null;
    }
}

record ReportRequest(String query) {}
record ReportResponse(String summary) {}
