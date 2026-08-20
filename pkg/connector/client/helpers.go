package client

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// IsNotFoundError reports whether err represents an upstream "not found" (HTTP
// 404) response. The uhttp client maps HTTP status codes onto gRPC status
// codes, so error classification lives here and nowhere else.
func IsNotFoundError(err error) bool {
	return status.Code(err) == codes.NotFound
}

// IsPermissionDeniedError reports whether err represents an upstream 403.
// Lucid's GET /v1/users/{id} returns 403 — never 404 — for a user that does not
// exist, so a 403 means either "gone" or "not permitted" and callers must
// disambiguate elsewhere. https://lucid.readme.io/reference/getuser
func IsPermissionDeniedError(err error) bool {
	return status.Code(err) == codes.PermissionDenied
}

// IsConflictError reports whether err represents an upstream 409. SCIM delete
// uses it for a user that can never be deleted (account owner, default document
// owner) — terminal, not an idempotent "already done".
// This is the connector's single 409 predicate; there is intentionally no
// separate IsAlreadyExistsError so the two cannot drift apart.
func IsConflictError(err error) bool {
	return status.Code(err) == codes.AlreadyExists
}

// IsPermissionDeniedError reports whether err represents an upstream 403.
// Lucid's GET /v1/users/{id} returns 403 — never 404 — for a user that does not
// exist, so a 403 means either "gone" or "not permitted" and callers must
// disambiguate elsewhere. https://lucid.readme.io/reference/getuser
func IsPermissionDeniedError(err error) bool {
	return status.Code(err) == codes.PermissionDenied
}

// IsConflictError reports whether err represents an upstream 409. SCIM delete
// uses it for a user that can never be deleted (account owner, default document
// owner) — terminal, not an idempotent "already done".
func IsConflictError(err error) bool {
	return status.Code(err) == codes.AlreadyExists
}
