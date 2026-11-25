package qdata

import (
	"context"
	"slices"
	"strings"
)

// Identity is authenticated request state held outside the wire query.
type Identity struct {
	Authority string
	Principal string
	Tenant    string
	Groups    []string
	Roles     []string
	Scopes    []string
}

type identityKey struct{}

func (i Identity) clone() Identity {
	i.Groups = slices.Clone(i.Groups)
	i.Roles = slices.Clone(i.Roles)
	i.Scopes = slices.Clone(i.Scopes)
	return i
}

// WithIdentity publishes a verifier's identity. Callers must validate it before publishing.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, identity.clone())
}

// IdentityFromContext retrieves a copy of the server-owned identity.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(Identity)
	return identity.clone(), ok
}

// HeaderValuesFor returns transport values after acceptor ingress sanitization.
func HeaderValuesFor(query *Query, name string) []string {
	var result []string
	for key, values := range query.GetHeader() {
		if strings.EqualFold(key, name) {
			result = append(result, values.GetValues()...)
		}
	}
	return result
}
