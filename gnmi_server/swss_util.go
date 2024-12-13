package gnmi

import (
	"google.golang.org/grpc/codes"
)

// Converts a SWSS error code string into a gRPC code.
func SwssToErrorCode(statusStr string) codes.Code {
	switch statusStr {
	case "SWSS_RC_SUCCESS":
		return codes.OK
	case "SWSS_RC_UNKNOWN":
		return codes.Unknown
	case "SWSS_RC_IN_USE", "SWSS_RC_INVALID_PARAM":
		return codes.InvalidArgument
	case "SWSS_RC_DEADLINE_EXCEEDED":
		return codes.DeadlineExceeded
	case "SWSS_RC_NOT_FOUND":
		return codes.NotFound
	case "SWSS_RC_EXISTS":
		return codes.AlreadyExists
	case "SWSS_RC_PERMISSION_DENIED":
		return codes.PermissionDenied
	case "SWSS_RC_FULL", "SWSS_RC_NO_MEMORY":
		return codes.ResourceExhausted
	case "SWSS_RC_UNIMPLEMENTED":
		return codes.Unimplemented
	case "SWSS_RC_INTERNAL":
		return codes.Internal
	case "SWSS_RC_NOT_EXECUTED", "SWSS_RC_FAILED_PRECONDITION":
		return codes.FailedPrecondition
	case "SWSS_RC_UNAVAIL":
		return codes.Unavailable
	}
	return codes.Internal
}