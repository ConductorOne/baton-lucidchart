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
// Lucid documents only 200 and 403 for GET /v1/users/{id}, with 403 covering
// both "does not belong to the authenticated account or does not exist" and a
// plain scope failure — so a documented 403 means either "gone" or "not
// permitted" and callers must disambiguate elsewhere.
// https://lucid.readme.io/reference/getuser
//
// An undocumented 404 has also been observed from that endpoint and must be
// handled defensively rather than trusted as proof of absence, so
// IsNotFoundError is not the complement of this predicate here. See
// transferContentBeforeDelete in pkg/connector/users.go, which disambiguates
// both responses through a SCIM existence probe before allowing a hard delete.
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

// IsRetryableError reports whether err carries a gRPC status code that the SDK's
// retry layer re-attempts. The SDK gate (vendor/.../pkg/retry/retry.go) treats
// exactly codes.Unavailable and codes.DeadlineExceeded as retryable, and
// GrpcCodeFromHTTPStatus maps HTTP 429/5xx (except 501 → Unimplemented) to
// Unavailable and HTTP 408 to DeadlineExceeded. Kept here alongside the other
// predicates so the classification cannot drift from the SDK gate.
func IsRetryableError(err error) bool {
	code := status.Code(err)
	return code == codes.Unavailable || code == codes.DeadlineExceeded
}
