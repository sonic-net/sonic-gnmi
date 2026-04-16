package gnmi

import (
	"testing"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestJwtAuthenAndAuthor_InvalidToken(t *testing.T) {
	md := metadata.New(map[string]string{
		"access_token": "not-a-valid-jwt-token",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, _, err := JwtAuthenAndAuthor(ctx)
	if err == nil {
		t.Fatal("Expected error for invalid JWT token, got nil")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("Expected Unauthenticated, got %v", st.Code())
	}
}
