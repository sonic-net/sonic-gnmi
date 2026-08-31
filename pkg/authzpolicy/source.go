package authzpolicy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/authz"
)

const (
	SourceFile     = "file"
	SourceConfigDB = "config_db"
)

// Source creates method authorization interceptors from one policy source.
type Source interface {
	Load(context.Context) (*PolicyInterceptor, error)
}

// PolicyInterceptor adapts grpc-go authorization to the server interceptor chain.
type PolicyInterceptor struct {
	unary     grpc.UnaryServerInterceptor
	stream    grpc.StreamServerInterceptor
	close     func()
	closeOnce sync.Once
}

func newPolicyInterceptor(unary grpc.UnaryServerInterceptor, stream grpc.StreamServerInterceptor, close func()) *PolicyInterceptor {
	return &PolicyInterceptor{unary: unary, stream: stream, close: close}
}

func (i *PolicyInterceptor) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return i.unary
}

func (i *PolicyInterceptor) StreamInterceptor() grpc.StreamServerInterceptor {
	return i.stream
}

func (i *PolicyInterceptor) Close() {
	i.closeOnce.Do(func() {
		if i.close != nil {
			i.close()
		}
	})
}

// FileSource preserves the existing grpc-go policy file watcher.
type FileSource struct {
	Path            string
	RefreshInterval time.Duration
}

func (s FileSource) Load(context.Context) (*PolicyInterceptor, error) {
	watcher, err := authz.NewFileWatcher(s.Path, s.RefreshInterval)
	if err != nil {
		return nil, fmt.Errorf("load file authorization policy: %w", err)
	}
	return newPolicyInterceptor(watcher.UnaryInterceptor, watcher.StreamInterceptor, watcher.Close), nil
}
