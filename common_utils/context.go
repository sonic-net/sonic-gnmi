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
	GNOI_REBOOT
	DBUS
	DBUS_FAIL
	DBUS_APPLY_PATCH_DB
	DBUS_APPLY_PATCH_YANG
	DBUS_CREATE_CHECKPOINT
	DBUS_DELETE_CHECKPOINT
	DBUS_CONFIG_SAVE
	DBUS_CONFIG_RELOAD
	DBUS_STOP_SERVICE
	DBUS_RESTART_SERVICE
	DBUS_FILE_STAT
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
	case GNOI_REBOOT:
		return "GNOI reboot"
	case DBUS:
		return "DBUS"
	case DBUS_FAIL:
		return "DBUS fail"
	case DBUS_APPLY_PATCH_DB:
		return "DBUS apply patch db"
	case DBUS_APPLY_PATCH_YANG:
		return "DBUS apply patch yang"
	case DBUS_CREATE_CHECKPOINT:
		return "DBUS create checkpoint"
	case DBUS_DELETE_CHECKPOINT:
		return "DBUS delete checkpoint"
	case DBUS_CONFIG_SAVE:
		return "DBUS config save"
	case DBUS_CONFIG_RELOAD:
		return "DBUS config reload"
	case DBUS_STOP_SERVICE:
		return "DBUS stop service"
	case DBUS_RESTART_SERVICE:
		return "DBUS restart service"
	case DBUS_FILE_STAT:
		return "DBUS file stat"
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

