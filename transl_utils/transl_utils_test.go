package transl_utils

import (
	"context"
	"errors"
	"testing"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Azure/sonic-mgmt-common/translib/tlerr"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
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
