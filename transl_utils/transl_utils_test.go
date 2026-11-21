package transl_utils

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Azure/sonic-mgmt-common/translib/tlerr"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestToStatus(t *testing.T) {

	ToStatus(nil)
	ToStatus(tlerr.AuthorizationError{})
	ToStatus(tlerr.TranslibSyntaxValidationError{
		StatusCode: 0,
		ErrorStr:   errors.New("Random syntax error occurred"),
	})
	ToStatus(tlerr.TranslibUnsupportedClientVersion{
		ClientVersion: "1.0",
	})
	ToStatus(tlerr.InternalError{
		Path: "something",
	})

	ToStatus(tlerr.NotFoundError{
		Path: "something",
	})
	ToStatus(tlerr.AlreadyExistsError{
		Path: "something",
	})
	ToStatus(tlerr.TranslibCVLFailure{
		Code: 1001,
	})
	ToStatus(tlerr.TranslibTransactionFail{})
	ToStatus(tlerr.TranslibRedisClientEntryNotExist{
		Entry: "Redis",
	})
	ToStatus(tlerr.TranslibDBScriptFail{
		Description: "script fail",
	})
}

func TestTranslProcessGet_Success_Proto(t *testing.T) {
	// 1. Save original function and restore after test
	origGet := translibGet
	defer func() { translibGet = origGet }()

	// 2. Define the mock behavior
	expectedPayload := []byte("mock data")
	translibGet = func(req translib.GetRequest) (translib.GetResponse, error) {
		return translib.GetResponse{
			Payload: expectedPayload,
		}, nil
	}

	// 3. Setup context (ensure common_utils.GetContext won't crash)
	// You might need to populate the context with Auth/Bundle version if your code reads it
	ctx := context.Background()

	// 4. Execute
	typedVal, resp, err := TranslProcessGet("/access-list", nil, ctx, gnmipb.Encoding_PROTO)

	// 5. Assertions
	assert.NoError(t, err)
	assert.Nil(t, typedVal, "PROTO encoding should return nil for TypedValue")
	assert.NotNil(t, resp, "PROTO encoding should return the translib response")
	assert.Equal(t, expectedPayload, resp.Payload)
}

func TestTranslProcessGet_Success_JSON(t *testing.T) {
	origGet := translibGet
	defer func() { translibGet = origGet }()

	translibGet = func(req translib.GetRequest) (translib.GetResponse, error) {
		return translib.GetResponse{
			Payload: []byte(`{ "foo": "bar" }`),
		}, nil
	}

	typedVal, _, err := TranslProcessGet("/any-path", nil, context.Background(), gnmipb.Encoding_JSON_IETF)

	assert.NoError(t, err)
	assert.NotNil(t, typedVal)
	// Check for compacted JSON (no spaces)
	assert.Equal(t, []byte(`{"foo":"bar"}`), typedVal.GetJsonIetfVal())
}

const setMtuPath = "/openconfig-interfaces:interfaces/interface[name=Ethernet0]/config/mtu"

func ietfValue(s string) *gnmipb.TypedValue {
	return &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(s)}}
}

// mtuUpdate returns an update of the mtu leaf, with its path validated by
// ConvertToURI as in a Set request.
func mtuUpdate(t *testing.T, val *gnmipb.TypedValue) *gnmipb.Update {
	t.Helper()
	path, err := ygot.StringToStructuredPath(setMtuPath)
	if err != nil {
		t.Fatalf("bad path: %v", err)
	}
	u := &gnmipb.Update{Path: path, Val: val}
	if _, err := ConvertToURI(nil, u.GetPath()); err != nil {
		t.Fatalf("ConvertToURI() error = %v", err)
	}
	return u
}

// leafBody decodes a translib body of one leaf and returns its name and value.
func leafBody(t *testing.T, body []byte) (string, string) {
	t.Helper()
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		t.Fatalf("body %s is not a JSON object: %v", body, err)
	}
	if len(obj) != 1 {
		t.Fatalf("body %s has %d members, want 1", body, len(obj))
	}
	for k, v := range obj {
		return k, string(v)
	}
	return "", ""
}

func TestSetPayload(t *testing.T) {
	t.Run("gNMI value of a leaf is wrapped in its name", func(t *testing.T) {
		for _, replace := range []bool{true, false} {
			body, err := setPayload(nil, mtuUpdate(t, ietfValue(`9100`)), replace)
			assert.NoError(t, err)
			name, value := leafBody(t, body)
			assert.True(t, strings.HasSuffix(name, "mtu"), "member name %q", name)
			assert.Equal(t, "9100", value)
		}
	})

	t.Run("scalar value of a leaf", func(t *testing.T) {
		val := &gnmipb.TypedValue{Value: &gnmipb.TypedValue_UintVal{UintVal: 9100}}
		body, err := setPayload(nil, mtuUpdate(t, val), false)
		assert.NoError(t, err)
		_, value := leafBody(t, body)
		assert.Equal(t, "9100", value)
	})

	t.Run("prefix and path are joined", func(t *testing.T) {
		full, err := ygot.StringToStructuredPath(setMtuPath)
		if err != nil {
			t.Fatal(err)
		}
		prefix := &gnmipb.Path{Elem: append([]*gnmipb.PathElem(nil), full.Elem[:2]...)}
		u := &gnmipb.Update{Path: &gnmipb.Path{Elem: append([]*gnmipb.PathElem(nil), full.Elem[2:]...)}, Val: ietfValue(`9100`)}
		if _, err := ConvertToURI(prefix, u.GetPath()); err != nil {
			t.Fatalf("ConvertToURI() error = %v", err)
		}
		body, err := setPayload(prefix, u, true)
		assert.NoError(t, err)
		_, value := leafBody(t, body)
		assert.Equal(t, "9100", value)
	})

	t.Run("invalid JSON is rejected", func(t *testing.T) {
		_, err := setPayload(nil, mtuUpdate(t, ietfValue(`{"mtu":`)), true)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("data after the JSON value is rejected", func(t *testing.T) {
		for _, v := range []string{`9100]`, `9100}`, `9100 9100`} {
			_, err := setPayload(nil, mtuUpdate(t, ietfValue(v)), true)
			assert.Equal(t, codes.InvalidArgument, status.Code(err), v)
		}
	})

	t.Run("json_val of a leaf", func(t *testing.T) {
		val := &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonVal{JsonVal: []byte(`9100`)}}
		body, err := setPayload(nil, mtuUpdate(t, val), false)
		assert.NoError(t, err)
		_, value := leafBody(t, body)
		assert.Equal(t, "9100", value)
	})

	t.Run("missing value is rejected", func(t *testing.T) {
		_, err := setPayload(nil, mtuUpdate(t, nil), false)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// A value that cannot be converted fails the request before translib is
// called, for each Set operation.
func TestTranslProcessSetRejectsInvalidValue(t *testing.T) {
	ctx := context.Background()
	bad := func() *gnmipb.Update { return mtuUpdate(t, ietfValue(`{"mtu":`)) }

	t.Run("replace", func(t *testing.T) {
		err := TranslProcessReplace(nil, bad(), ctx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
	t.Run("update", func(t *testing.T) {
		err := TranslProcessUpdate(nil, bad(), ctx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
	t.Run("bulk replace", func(t *testing.T) {
		err := TranslProcessBulk(nil, []*gnmipb.Update{bad()}, nil, nil, ctx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
	t.Run("bulk update", func(t *testing.T) {
		err := TranslProcessBulk(nil, nil, []*gnmipb.Update{bad()}, nil, ctx)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}
