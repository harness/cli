package com.example.incidents;

import javax.ws.rs.POST;
import javax.ws.rs.Path;

@Path("/api/v1/incidents")
public class IncidentResource {
    @POST
    @Path("/analyze")
    public IncidentResponse analyze(IncidentRequest request) {
        return null;
    }
}

record IncidentRequest(String query) {}
record IncidentResponse(String analysis) {}
