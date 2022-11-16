package common_utils

import (
	"context"
	"fmt"
	"sync/atomic"
)


// AuthInfo holds data about the authenticated user
type AuthInfo struct {
	// Username
	User string
	AuthEnabled bool
	// Roles
	Roles []string
}

// RequestContext holds metadata about REST request.
type RequestContext struct {

	// Unique reqiest id
	ID string

	// Auth contains the authorized user information
	Auth AuthInfo

	//Bundle Version is the release yang models version.
	BundleVersion *string
}

type contextkey int

const requestContextKey contextkey = 0

// Request Id generator
var requestCounter uint64

type CounterType int
const (
	GNMI_GET CounterType = iota
	GNMI_GET_FAIL
	GNMI_SET
	GNMI_SET_FAIL
	COUNTER_SIZE
)

func (c CounterType) String() string {
	switch c {
	case GNMI_GET:
		return "GNMI get"
	case GNMI_GET_FAIL:
		return "GNMI get fail"
	case GNMI_SET:
		return "GNMI set"
	case GNMI_SET_FAIL:
		return "GNMI set fail"
	default:
		return ""
	}
}

var globalCounters [COUNTER_SIZE]uint64


// GetContext function returns the RequestContext object for a
// gRPC request. RequestContext is maintained as a context value of
// the request. Creates a new RequestContext object is not already
// available.
func GetContext(ctx context.Context) (*RequestContext, context.Context) {
	cv := ctx.Value(requestContextKey)
	if cv != nil {
		return cv.(*RequestContext), ctx
	}

	rc := new(RequestContext)
	rc.ID = fmt.Sprintf("TELEMETRY-%v", atomic.AddUint64(&requestCounter, 1))

	ctx = context.WithValue(ctx, requestContextKey, rc)
	return rc, ctx
}

func GetUsername(ctx context.Context, username *string) {
	rc, _ := GetContext(ctx)
	if rc != nil {
		*username = rc.Auth.User
	}
}

func InitCounters() {
	for i := 0; i < int(COUNTER_SIZE); i++ {
		globalCounters[i] = 0
	}
	SetMemCounters(&globalCounters)
}

func IncCounter(cnt CounterType) {
	atomic.AddUint64(&globalCounters[cnt], 1)
	SetMemCounters(&globalCounters)
}

