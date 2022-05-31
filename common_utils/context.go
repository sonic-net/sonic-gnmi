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

type ShareCounters struct {
    Gnmi_get_cnt uint64
    Gnmi_get_fail_cnt uint64
    Gnmi_set_cnt uint64
    Gnmi_set_fail_cnt uint64
	Gnoi_reboot_cnt uint64
	Dbus_cnt uint64
	Dbus_fail_cnt uint64
}

var globalCounters ShareCounters

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
	globalCounters.Gnmi_get_cnt = 0
	globalCounters.Gnmi_get_fail_cnt = 0
	globalCounters.Gnmi_set_cnt = 0
	globalCounters.Gnmi_set_fail_cnt = 0
	globalCounters.Gnoi_reboot_cnt = 0
	globalCounters.Dbus_cnt = 0
	globalCounters.Dbus_fail_cnt = 0
	SetMemCounters(&globalCounters)
}

func IncGnmiGetCnt() {
	atomic.AddUint64(&globalCounters.Gnmi_get_cnt, 1)
	SetMemCounters(&globalCounters)
}

func IncGnmiGetFailCnt() {
	atomic.AddUint64(&globalCounters.Gnmi_get_fail_cnt, 1)
	SetMemCounters(&globalCounters)
}

func IncGnmiSetCnt() {
	atomic.AddUint64(&globalCounters.Gnmi_set_cnt, 1)
	SetMemCounters(&globalCounters)
}

func IncGnmiSetFailCnt() {
	atomic.AddUint64(&globalCounters.Gnmi_set_fail_cnt, 1)
	SetMemCounters(&globalCounters)
}

func IncGnoiRebootCnt() {
	atomic.AddUint64(&globalCounters.Gnoi_reboot_cnt, 1)
	SetMemCounters(&globalCounters)
}

func IncDbusCnt() {
	atomic.AddUint64(&globalCounters.Dbus_cnt, 1)
	SetMemCounters(&globalCounters)
}

func IncDbusFailCnt() {
	atomic.AddUint64(&globalCounters.Dbus_fail_cnt, 1)
	SetMemCounters(&globalCounters)
}
