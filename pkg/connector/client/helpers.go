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
