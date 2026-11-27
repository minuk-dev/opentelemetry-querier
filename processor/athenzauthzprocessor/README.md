# Athenz authorization

Place `athenzauthz` in the query pipeline. Set its `authenticator` to the ID
of an enabled `athenzauth` extension for the domain whose signed policies
should be loaded. It checks the verified roles against
`<domain>:<resource_prefix>.<signal>` for every queried signal. Both `action`
and `resource_prefix` default to `query`. Policy freshness is controlled by
`refresh_interval` and `max_policy_age`.

Set `enforce_label` to constrain results to the authenticated tenant. Include
`queryrewrite` after this processor to apply the matcher.
