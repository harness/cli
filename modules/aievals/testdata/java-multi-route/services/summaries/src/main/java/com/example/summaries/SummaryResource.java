package com.example.summaries;

import javax.ws.rs.POST;
import javax.ws.rs.Path;

@Path("/api/v1/summaries")
public class SummaryResource {
    @POST
    @Path("/summarize")
    public SummaryResponse summarize(SummaryRequest request) {
        return null;
    }
}

record SummaryRequest(String document) {}
record SummaryResponse(String summary) {}
