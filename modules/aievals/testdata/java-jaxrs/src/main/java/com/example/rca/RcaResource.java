package com.example.rca;

import javax.ws.rs.POST;
import javax.ws.rs.Path;
import javax.ws.rs.Produces;
import javax.ws.rs.core.MediaType;

@Path("/api/v1/rc/evals/rca")
public class RcaResource {
    @POST
    @Path("/analyze")
    @Produces(MediaType.APPLICATION_JSON)
    public RcaResponse analyze(RcaRequest request) {
        return null;
    }
}
