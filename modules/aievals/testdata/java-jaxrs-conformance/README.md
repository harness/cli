# JAX-RS conformance fixture

This fixture is framework-neutral and intentionally contains only source
evidence: a class-level and method-level `@Path`, `@POST`, a route-local
`@HarnessAuth`, required query parameters, a typed record request, and a
typed response whose first text field is `reasoning`.

It does not contain credentials, environment values, service ports, or
product-specific onboarding configuration.
