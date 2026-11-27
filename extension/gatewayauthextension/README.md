# Trusted gateway authentication

Enable a named `gatewayauth` instance in `service.extensions` and point an
acceptor's `auth.authenticator` at it. Set `authority` and point
`secret_file` at a file containing the gateway secret (at least 32 bytes).

The gateway must strip caller-supplied assertions before setting
`X-Querier-Gateway-Secret`, `X-Querier-Principal`, and one or more
`X-Querier-Allowed-Tenant` values. It may also assert `X-Querier-Group` and
`X-Querier-Role`. Use encrypted transport for these headers.
