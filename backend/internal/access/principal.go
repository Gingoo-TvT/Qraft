// Package access carries the authenticated server identity across service calls.
package access

import "context"

type Principal struct {
	UserID string
	Role   string
}
type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok && p.UserID != "" && (p.Role == "admin" || p.Role == "member")
}
func (p Principal) IsAdmin() bool { return p.UserID != "" && p.Role == "admin" }
