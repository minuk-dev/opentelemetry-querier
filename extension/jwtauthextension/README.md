# JWT authentication

Configure a `jwtauth` instance with `issuer`, `audience`, allowed
`algorithms` (RS256, RS384 or RS512), `tenant_claim`, and one of
`public_key_file` and an HTTPS `jwks_url`. Optional `group_claim` and
`role_claim` values map verified claims into the principal. Enable the
instance in `service.extensions` and use its ID in the acceptor's
`auth.authenticator`.

Evaluate permissions on the verified principal in pipeline processors.
