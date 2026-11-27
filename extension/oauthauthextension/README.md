# OAuth 2.0 access token authentication

Enable an `oauthauth` extension in `service.extensions` and set the
acceptor's `auth.authenticator` to its ID. Configure either `jwt` for RFC
9068 access tokens or `introspection` for opaque tokens.

The JWT settings use the [JWT extension's configuration](../jwtauthextension).
Introspection takes `endpoint` (HTTPS), `issuer`, `audience`, `tenant_claim`,
`client_id`, `client_secret_file`, and an optional `timeout`.

The extension publishes verified scopes. Add
[`scopeauthz`](../../processor/scopeauthzprocessor) to the pipeline to
enforce scopes. In standalone manifests, include the JWT extension at the
same release as the OAuth extension so both modules are pinned.
