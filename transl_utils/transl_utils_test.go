package transl_utils

import (
	"errors"
	"testing"

	"github.com/Azure/sonic-mgmt-common/translib/tlerr"
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
