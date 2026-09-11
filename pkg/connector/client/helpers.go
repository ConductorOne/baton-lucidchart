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
// Lucid's GET /v1/users/{id} returns 403 for both "not permitted" and "does
// not exist" (https://lucid.readme.io/reference/getuser), so callers must
// disambiguate elsewhere.
func IsPermissionDeniedError(err error) bool {
	return status.Code(err) == codes.PermissionDenied
}

// IsConflictError reports whether err represents an upstream 409. SCIM delete
// uses it for a user that can never be deleted (account owner, default
// document owner) — terminal, not an idempotent "already done".
func IsConflictError(err error) bool {
	return status.Code(err) == codes.AlreadyExists
}

// IsRetryableError reports whether err carries a gRPC code the SDK's retry
// layer re-attempts (codes.Unavailable or codes.DeadlineExceeded).
func IsRetryableError(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
