package dpuproxy

import (
	"context"

	"github.com/golang/glog"
	"google.golang.org/grpc"
)

// DPUProxy is a gRPC interceptor that routes requests to DPU targets based on metadata.
// It examines incoming gRPC metadata for x-sonic-ss-target-type and x-sonic-ss-target-index
// headers and routes requests accordingly.
//
// Current implementation (Phase 1.5): Resolves DPU info from Redis and logs it.
// Future implementation (Phase 2): Will proxy requests to actual DPU gRPC servers.
type DPUProxy struct {
	resolver *DPUResolver
	// Future fields for Phase 2:
	// - gRPC client pool for connection management
	// - Configuration options
}

// NewDPUProxy creates a new DPU proxy interceptor with the given resolver.
// If resolver is nil, Redis operations will be skipped (for testing).
func NewDPUProxy(resolver *DPUResolver) *DPUProxy {
	return &DPUProxy{
		resolver: resolver,
	}
}

// UnaryInterceptor returns a gRPC unary server interceptor for DPU routing.
// It intercepts unary RPC calls and checks for routing metadata.
func (p *DPUProxy) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract routing metadata
		targetMeta := ExtractTargetMetadata(ctx)

		// If DPU routing is requested, resolve DPU info
		if targetMeta.IsDPUTarget() {
			glog.Infof("[DPUProxy] DPU routing requested: method=%s, target-type=%s, target-index=%s",
				info.FullMethod, targetMeta.TargetType, targetMeta.TargetIndex)

			// Resolve DPU information from Redis (if resolver is available)
			if p.resolver != nil {
				dpuInfo, err := p.resolver.GetDPUInfo(ctx, targetMeta.TargetIndex)
				if err != nil {
					glog.Warningf("[DPUProxy] Error resolving DPU%s: %v, passing through to local handler",
						targetMeta.TargetIndex, err)
				} else {
					glog.Infof("[DPUProxy] Resolved DPU%s: ip=%s, reachable=%t",
						dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.Reachable)

					if !dpuInfo.Reachable {
						glog.Warningf("[DPUProxy] DPU%s is unreachable, passing through to local handler",
							dpuInfo.Index)
					} else {
						// TODO (Phase 2): Implement actual DPU proxying
						// 1. Get or create gRPC client connection to dpuInfo.IPAddress:8080
						// 2. Forward the request to DPU
						// 3. Return DPU response
						glog.Infof("[DPUProxy] TODO: Forward request to %s:8080 (not implemented yet)",
							dpuInfo.IPAddress)
					}
				}
			}
		}

		// Pass through to the next handler in the chain
		return handler(ctx, req)
	}
}

// StreamInterceptor returns a gRPC stream server interceptor for DPU routing.
// It intercepts streaming RPC calls and checks for routing metadata.
func (p *DPUProxy) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Extract routing metadata from the stream context
		ctx := ss.Context()
		targetMeta := ExtractTargetMetadata(ctx)

		// If DPU routing is requested, resolve DPU info
		if targetMeta.IsDPUTarget() {
			glog.Infof("[DPUProxy] DPU streaming requested: method=%s, target-type=%s, target-index=%s",
				info.FullMethod, targetMeta.TargetType, targetMeta.TargetIndex)

			// Resolve DPU information from Redis (if resolver is available)
			if p.resolver != nil {
				dpuInfo, err := p.resolver.GetDPUInfo(ctx, targetMeta.TargetIndex)
				if err != nil {
					glog.Warningf("[DPUProxy] Error resolving DPU%s: %v, passing through to local handler",
						targetMeta.TargetIndex, err)
				} else {
					glog.Infof("[DPUProxy] Resolved DPU%s: ip=%s, reachable=%t",
						dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.Reachable)

					if !dpuInfo.Reachable {
						glog.Warningf("[DPUProxy] DPU%s is unreachable, passing through to local handler",
							dpuInfo.Index)
					} else {
						// TODO (Phase 2): Implement actual DPU streaming proxy
						// 1. Get or create gRPC client connection to dpuInfo.IPAddress:8080
						// 2. Forward the stream to DPU
						// 3. Bidirectionally relay messages between client and DPU
						glog.Infof("[DPUProxy] TODO: Forward stream to %s:8080 (not implemented yet)",
							dpuInfo.IPAddress)
					}
				}
			}
		}

		// Pass through to the next handler in the chain
		return handler(srv, ss)
	}
}
