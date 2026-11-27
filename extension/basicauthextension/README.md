# Basic authentication

Declare a `basicauth` extension with `authority`, `realm` and an `accounts`
map keyed by username. Each account names a mounted bcrypt `hash_file` and
its allowed `tenants`. Enable the instance in `service.extensions` and set
the acceptor's `auth.authenticator` to its ID.

Use encrypted transport. The HTTP acceptors send a Basic challenge for
invalid credentials. Configure query permissions in a processor.
