# qerror

Transport-agnostic, coded errors that flow out of processors and the pipeline.

A processor returns a `*qerror.Error` to short-circuit the pipeline (auth
failure, rate limit, …). Each carries a `Code` (loosely aligned with gRPC codes)
that acceptors map onto their transport:

- `HTTPStatus()` → an HTTP status code (used by the OTQP/HTTP acceptor).
- `CodeOf(err)` → extracts the `Code` (used to build a gRPC status).

Keeping this type in its own module avoids import cycles between the pipeline,
processors and acceptors.
