# Static bearer authentication

Configure one extension instance per service identity. A token presented
in `Authorization: Bearer <token>` grants that instance's principal and tenant
scope.

```yaml
extensions:
  bearertokenauth/service:
    authority: local
    principal: service
    tenants: [acme]
    tokens: [development-token]
acceptors:
  otqp:
    auth:
      authenticator: bearertokenauth/service
service:
  extensions: [bearertokenauth/service]
```

Use deployment-managed credentials and encrypted transport. Evaluate query
policy in the pipeline's processors.
