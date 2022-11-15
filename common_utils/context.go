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

var CountersName = [...]string {
	"GNMI get",
	"GNMI get fail",
	"GNMI set",
	"GNMI set fail",
	"GNOI reboot",
	"DBUS",
	"DBUS fail",
	"DBUS apply patch db",
	"DBUS apply patch yang",
	"DBUS create checkpoint",
	"DBUS delete checkpoint",
	"DBUS config save",
	"DBUS config reload",
}

var globalCounters [len(CountersName)]uint64


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
	for i := 0; i < len(CountersName); i++ {
		globalCounters[i] = 0
	}
	SetMemCounters(&globalCounters)
}

func IncCounter(name string) {
	for i := 0; i < len(CountersName); i++ {
		if CountersName[i] == name {
			atomic.AddUint64(&globalCounters[i], 1)
			break
		}
	}
	SetMemCounters(&globalCounters)
}

