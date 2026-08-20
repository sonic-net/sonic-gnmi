package transl_utils

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/sonic-mgmt-common/cvl"
	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Azure/sonic-mgmt-common/translib/tlerr"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestErrorPath(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "InvalidArgsError",
			err:  tlerr.InvalidArgsErr("", "/acl/set1", "invalid args"),
			want: "/acl/set1",
		},
		{
			name: "NotSupportedError",
			err:  tlerr.NotSupportedErr("", "/feature/x", "not supported"),
			want: "/feature/x",
		},
		{
			name: "NotFoundError",
			err:  tlerr.NotFoundErr("", "/missing", "not found"),
			want: "/missing",
		},
		{
			name: "AlreadyExistsError",
			err:  tlerr.AlreadyExistsErr("", "/exists", "already exists"),
			want: "/exists",
		},
		{
			name: "InternalError",
			err:  tlerr.NewError("", "/internal", "internal error"),
			want: "/internal",
		},
		{
			name: "AuthorizationError",
			err:  tlerr.AuthorizationError{Path: "/denied"},
			want: "/denied",
		},
		{
			name: "unknown",
			err:  errors.New("generic"),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, errorPath(tt.err))
		})
	}
}

func TestFormatErrorWithPath(t *testing.T) {
	assert.Equal(t, "failed", formatErrorWithPath("failed", ""))
	assert.Equal(t, "failed\n  request path: /foo", formatErrorWithPath("failed", "/foo"))
}

func TestToStatus_InvalidArgsAndNotSupported(t *testing.T) {
	invalid := ToStatus(tlerr.InvalidArgsErr("", "/path/invalid", "bad argument"))
	assert.Equal(t, codes.InvalidArgument, invalid.Code())
	assert.Contains(t, invalid.Message(), "bad argument")
	assert.Contains(t, invalid.Message(), "request path: /path/invalid")

	unsupported := ToStatus(tlerr.NotSupportedErr("", "/path/unsupported", "unsupported op"))
	assert.Equal(t, codes.InvalidArgument, unsupported.Code())
	assert.Contains(t, unsupported.Message(), "unsupported op")
	assert.Contains(t, unsupported.Message(), "request path: /path/unsupported")
}

func TestToStatus_CVLFailureWithTableAndKeys(t *testing.T) {
	st := ToStatus(tlerr.TranslibCVLFailure{
		CVLErrorInfo: cvl.CVLErrorInfo{
			Msg:       "range violation",
			TableName: "ACL_TABLE",
			Keys:      []string{"TEST"},
		},
	})
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "range violation")
	assert.Contains(t, st.Message(), "table: ACL_TABLE")
	assert.Contains(t, st.Message(), "keys: [TEST]")
}

func mustDeletePath(t *testing.T) *gnmipb.Path {
	t.Helper()
	p, err := ygot.StringToPath(
		"/openconfig-acl:acl/acl-sets/acl-set[name=TEST][type=ACL_IPV4]",
		ygot.StructuredPath,
		ygot.StringSlicePath,
	)
	if err != nil {
		t.Fatalf("StringToPath: %v", err)
	}
	return p
}

func TestTranslProcessBulk_EntryError(t *testing.T) {
	origBulk := translibBulk
	defer func() { translibBulk = origBulk }()

	delPath := mustDeletePath(t)
	entryErr := tlerr.InvalidArgsErr("", "", "delete failed")

	translibBulk = func(br translib.BulkRequest) (translib.BulkResponse, error) {
		if len(br.Request) != 1 {
			t.Fatalf("expected 1 bulk request, got %d", len(br.Request))
		}
		return translib.BulkResponse{
			Response: []translib.BulkResponseEntry{
				{
					Operation: translib.DELETE,
					Entry: translib.SetResponse{
						Err: entryErr,
					},
				},
			},
		}, nil
	}

	err := TranslProcessBulk([]*gnmipb.Path{delPath}, nil, nil, nil, context.Background())
	if err == nil {
		t.Fatal("expected bulk entry error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.True(t, strings.HasPrefix(st.Message(), "SET failed:"))
	assert.Contains(t, st.Message(), "delete failed")
	assert.Contains(t, st.Message(), brRequestPath(t, delPath))
}

func TestTranslProcessBulk_GlobalError(t *testing.T) {
	origBulk := translibBulk
	defer func() { translibBulk = origBulk }()

	delPath := mustDeletePath(t)
	globalErr := tlerr.NotFoundErr("", "/missing", "resource missing")

	translibBulk = func(br translib.BulkRequest) (translib.BulkResponse, error) {
		return translib.BulkResponse{}, globalErr
	}

	err := TranslProcessBulk([]*gnmipb.Path{delPath}, nil, nil, nil, context.Background())
	if err == nil {
		t.Fatal("expected global bulk error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got %T: %v", err, err)
	}
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "resource missing")
}

func brRequestPath(t *testing.T, delPath *gnmipb.Path) string {
	t.Helper()
	uri, err := ConvertToURI(nil, delPath)
	if err != nil {
		t.Fatalf("ConvertToURI: %v", err)
	}
	return uri
}

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
