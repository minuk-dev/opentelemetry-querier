package qdata

// ValidIdentifier reports whether name is a bare identifier in the classic
// PromQL/LogQL grammar: `[a-zA-Z_][a-zA-Z0-9_]*`.
//
// Text-rendering dispatchers interpolate plan identifiers — label names,
// grouping/vector-matching label lists, function names — straight into the query
// string, where matcher *values* are protected by quoting but identifiers have no
// syntactic delimiter to escape into. Since Query.plan arrives off the wire, an
// identifier carrying `}`, `(`, whitespace or an operator lets a caller close the
// construct it sits in and append its own, escaping the enforced matchers that
// queryrewrite composed into the very same selector. Rendering identifiers is
// therefore gated on this predicate and fails closed (CodeInvalidArgument) rather
// than emitting text the renderer did not intend.
//
// The grammar is deliberately the classic one, so a dotted OpenTelemetry
// attribute name (`http.status_code`) is rejected. Such a name has no unquoted
// PromQL/LogQL spelling anyway — it would render as broken syntax today — so
// rejecting it is strictly better than shipping it. Supporting them properly
// means emitting Prometheus 3.x UTF-8 quoted label names, which is a separate
// change with an upstream-version requirement.
func ValidIdentifier(name string) bool {
	if name == "" {
		return false
	}

	for index, char := range name {
		switch {
		case char == '_',
			char >= 'a' && char <= 'z',
			char >= 'A' && char <= 'Z':
			continue
		case index > 0 && char >= '0' && char <= '9':
			continue
		default:
			return false
		}
	}

	return true
}
