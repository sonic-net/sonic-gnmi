package file

import (
	"context"
	"testing"
)

func TestHandleFileRemove_NilRequest(t *testing.T) {
	resp, err := HandleFileRemove(context.Background(), nil)
	if err == nil {
		t.Error("Expected error for nil request, got nil")
	}
	if resp != nil {
		t.Error("Expected nil response for nil request, got non-nil")
	}
}
