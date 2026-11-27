# Athenz authentication

Configure an `athenzauth` extension with an HTTPS `zts_url`, `domain` and
`tenant`. Enable it in `service.extensions` and reference it from the
acceptor's `auth.authenticator`. It verifies the `Athenz-Role-Auth` role token
and publishes the verified subject, roles and tenant. Optional
`refresh_interval` and `max_key_age` control the key refresh and its freshness.

Use the [Athenz authorization processor](../../processor/athenzauthzprocessor)
to check query access against signed domain policies.
