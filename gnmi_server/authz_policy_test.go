package gnmi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openconfig/gnsi/authz"
	"github.com/sonic-net/sonic-gnmi/pkg/authzpolicy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestLoadAuthzPolicyFileSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"name":"test","allow_rules":[{"name":"allow-all"}]}`), 0600); err != nil {
		t.Fatal(err)
	}

	policy, err := loadAuthzPolicy(&Config{
		AuthzPolicySource: authzpolicy.SourceFile,
		AuthzPolicyFile:   path,
	})
	if err != nil {
		t.Fatalf("loadAuthzPolicy() failed: %v", err)
	}
	policy.Close()
}

func TestLoadAuthzPolicyRejectsUnknownSource(t *testing.T) {
	_, err := loadAuthzPolicy(&Config{AuthzPolicySource: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "unsupported authorization policy source") {
		t.Fatalf("loadAuthzPolicy() error = %v, want unsupported source", err)
	}
}

type authzRotateTestStream struct {
	ctx context.Context
	grpc.ServerStream
}

func (s *authzRotateTestStream) Context() context.Context {
	return s.ctx
}

func (s *authzRotateTestStream) Recv() (*authz.RotateAuthzRequest, error) {
	return nil, context.Canceled
}

func (s *authzRotateTestStream) Send(*authz.RotateAuthzResponse) error {
	return nil
}

func TestConfigDBAuthzRotateAuthenticatesBeforeRejectingSecondWriter(t *testing.T) {
	original := authenticateFunc
	t.Cleanup(func() { authenticateFunc = original })
	authenticateFunc = func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
		return ctx, nil
	}

	server := &GNSIAuthzServer{Server: &Server{config: &Config{AuthzPolicySource: authzpolicy.SourceConfigDB}}}
	err := server.Rotate(&authzRotateTestStream{ctx: metadata.NewIncomingContext(context.Background(), metadata.MD{})})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("Rotate() status = %v, want FailedPrecondition", status.Code(err))
	}
}

func TestConfigDBAuthzRotateReturnsAuthenticationFailure(t *testing.T) {
	original := authenticateFunc
	t.Cleanup(func() { authenticateFunc = original })
	authenticateFunc = func(_ *Config, ctx context.Context, _ string, _ bool) (context.Context, error) {
		return ctx, status.Error(codes.Unauthenticated, "test rejection")
	}

	server := &GNSIAuthzServer{Server: &Server{config: &Config{AuthzPolicySource: authzpolicy.SourceConfigDB}}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := server.Rotate(&authzRotateTestStream{ctx: ctx})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("Rotate() status = %v, want Unauthenticated", status.Code(err))
	}
}
