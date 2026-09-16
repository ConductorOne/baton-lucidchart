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

// IsUnauthenticatedError reports whether err represents an upstream 401 — a
// bearer token that is wrong, expired, or not entitled to the surface it was
// sent to.
func IsUnauthenticatedError(err error) bool {
	return status.Code(err) == codes.Unauthenticated
}

// IsConflictError reports whether err represents an upstream 409. What a 409
// means is call-site specific: SCIM delete treats it as terminal (a user that
// can never be deleted — account owner, default document owner); folder/document
// Grant() treats it as an idempotent "already granted" no-op only when the
// conflicting record's role matches what was requested (or the record carries no
// role, unless the pre-check already confirmed the user holds no share, in which
// case a decoded matching role is required), and surfaces the error otherwise.
// The classifier only reports the status; the caller decides.
func IsConflictError(err error) bool {
	return status.Code(err) == codes.AlreadyExists
}

// IsRetryableError reports whether err carries a gRPC code the SDK's retry
// layer re-attempts (codes.Unavailable or codes.DeadlineExceeded).
func IsRetryableError(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
