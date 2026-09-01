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

// IsAlreadyExistsError reports whether err represents an upstream "already
// exists" (HTTP 409 Conflict) response.
func IsAlreadyExistsError(err error) bool {
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
