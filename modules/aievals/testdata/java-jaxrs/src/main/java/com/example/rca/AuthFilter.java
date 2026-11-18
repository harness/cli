package com.example.rca;

import javax.ws.rs.container.ContainerRequestContext;
import javax.ws.rs.container.ContainerRequestFilter;
import javax.ws.rs.ext.Provider;

@Provider
public class AuthFilter implements ContainerRequestFilter {
    @Override
    public void filter(ContainerRequestContext requestContext) {
        String authorization = requestContext.getHeaderString("Authorization");
        String apiKey = requestContext.getHeaderString("x-api-key");
        if (authorization == null && apiKey == null) {
            throw new RuntimeException("missing auth");
        }
    }
}
