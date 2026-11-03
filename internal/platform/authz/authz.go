// Package authz defines the authenticated principal and permission checks.
// Authorization is deny-by-default: every handler must check a permission or
// an ownership policy explicitly.
package authz

import (
	"context"

	"github.com/google/uuid"
)

// Principal is the authenticated actor for a request.
type Principal struct {
	UserID             uuid.UUID
	Username           string
	FullName           string
	Email              string
	Roles              []string
	MaxRank            int
	Permissions        map[string]bool
	SessionID          uuid.UUID
	MFAVerified        bool
	RequiresMFA        bool
	MFAEnrolled        bool
	MustChangePassword bool
	IP                 string
	UserAgent          string
}

// Can reports whether the principal holds the permission.
func (p *Principal) Can(perm string) bool {
	return p != nil && p.Permissions[perm]
}

// CanAny reports whether the principal holds any of the permissions.
func (p *Principal) CanAny(perms ...string) bool {
	for _, perm := range perms {
		if p.Can(perm) {
			return true
		}
	}
	return false
}

// PermissionList returns permissions as a sorted-agnostic slice.
func (p *Principal) PermissionList() []string {
	out := make([]string, 0, len(p.Permissions))
	for k := range p.Permissions {
		out = append(out, k)
	}
	return out
}

type ctxKey struct{}

// WithPrincipal stores the principal in ctx.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal or nil.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// System returns a principal representing background jobs.
func System() *Principal {
	return &Principal{Username: "system", Permissions: map[string]bool{}}
}

// MFAVerifiedOrNotRequired reports whether the MFA gate is satisfied.
func (p *Principal) MFAVerifiedOrNotRequired() bool {
	return p != nil && (!p.RequiresMFA || p.MFAVerified)
}
