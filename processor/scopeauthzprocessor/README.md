# Scope authorization

Place `scopeauthz` in the pipeline's processors. Configure `issuer` and
`required_scopes`. The processor permits queries whose verified identity
comes from that issuer and holds every required scope.

```yaml
processors:
  scopeauthz:
    issuer: https://identity.example
    required_scopes: [query]
```
