package dpuproxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/golang/glog"
	system "github.com/openconfig/gnoi/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// ForwardableMethod represents a gRPC method that can be forwarded to DPU.
type ForwardableMethod struct {
	FullMethod  string
	Description string
	Enabled     bool
}

// defaultForwardableMethods is the registry of methods that can be forwarded to DPU.
// Initially all methods are disabled and must be explicitly enabled when ready.
var defaultForwardableMethods = []ForwardableMethod{
	{
		FullMethod:  "/gnoi.system.System/Time",
		Description: "Get current time from DPU (for testing)",
		Enabled:     true, // Enabled for Phase 2.1 testing
	},
	{
		FullMethod:  "/gnoi.system.System/Reboot",
		Description: "Reboot the DPU",
		Enabled:     false,
	},
	{
		FullMethod:  "/gnoi.system.System/SetPackage",
		Description: "Install software package on DPU",
		Enabled:     false,
	},
}

// DPUProxy is a gRPC interceptor that routes requests to DPU targets based on metadata.
// It examines incoming gRPC metadata for x-sonic-ss-target-type and x-sonic-ss-target-index
// headers and routes requests accordingly.
//
// Current implementation (Phase 2.1): Actual forwarding to DPU gRPC servers with connection management.
type DPUProxy struct {
	resolver *DPUResolver

	// Connection management
	connMu sync.RWMutex
	conns  map[string]*grpc.ClientConn // key: DPU index (e.g., "0", "1")

	// TODO (Future Phase): Add connection health monitoring
	// - Periodic health checks using grpc.ConnectivityState
	// - Recreate connections in TRANSIENT_FAILURE state
	// - Metrics for connection state transitions

	// TODO (Future Phase): Add graceful shutdown
	// - Implement Close() method to drain and close all connections
	// - Register with service lifecycle hooks
}

// NewDPUProxy creates a new DPU proxy interceptor with the given resolver.
// If resolver is nil, Redis operations will be skipped (for testing).
func NewDPUProxy(resolver *DPUResolver) *DPUProxy {
	return &DPUProxy{
		resolver: resolver,
		conns:    make(map[string]*grpc.ClientConn),
	}
}

// shouldForwardToDPU checks if a method is registered as forwardable and enabled.
func (p *DPUProxy) shouldForwardToDPU(method string) bool {
	for _, m := range defaultForwardableMethods {
		if m.FullMethod == method {
			return m.Enabled
		}
	}
	return false
}

// getConnection retrieves or creates a gRPC connection to the specified DPU.
// Connections are cached and reused. Uses keepalive settings for long-lived connections.
func (p *DPUProxy) getConnection(ctx context.Context, dpuIndex, ipAddress string) (*grpc.ClientConn, error) {
	// Check if we already have a connection
	p.connMu.RLock()
	if conn, ok := p.conns[dpuIndex]; ok {
		p.connMu.RUnlock()
		return conn, nil
	}
	p.connMu.RUnlock()

	// Need to create a new connection
	p.connMu.Lock()
	defer p.connMu.Unlock()

	// Double-check after acquiring write lock (another goroutine might have created it)
	if conn, ok := p.conns[dpuIndex]; ok {
		return conn, nil
	}

	// Create new connection with keepalive settings for long-lived connections
	target := fmt.Sprintf("%s:50052", ipAddress)
	glog.Infof("[DPUProxy] Creating new gRPC connection to DPU%s at %s", dpuIndex, target)

	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                10 * time.Second, // Send keepalive ping every 10s
			Timeout:             3 * time.Second,  // Wait 3s for ping ack before considering connection dead
			PermitWithoutStream: true,             // Send pings even when no active RPCs
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection to DPU%s at %s: %w", dpuIndex, target, err)
	}

	// Cache the connection for reuse
	p.conns[dpuIndex] = conn
	glog.Infof("[DPUProxy] Successfully created connection to DPU%s at %s", dpuIndex, target)

	return conn, nil
}

// forwardTimeRequest forwards a gNOI System.Time request to the DPU.
func (p *DPUProxy) forwardTimeRequest(ctx context.Context, conn *grpc.ClientConn, req interface{}) (interface{}, error) {
	// Type assert the request
	timeReq, ok := req.(*system.TimeRequest)
	if !ok {
		glog.Errorf("[DPUProxy] Invalid request type for Time method: %T", req)
		return nil, status.Errorf(codes.Internal,
			"invalid request type for Time method: expected *system.TimeRequest, got %T", req)
	}

	// Create System client
	client := system.NewSystemClient(conn)

	// Forward the request to DPU
	resp, err := client.Time(ctx, timeReq)
	if err != nil {
		glog.Errorf("[DPUProxy] Error forwarding Time request to DPU: %v", err)
		return nil, err
	}

	glog.Infof("[DPUProxy] Successfully forwarded Time request to DPU, response: %v", resp)
	return resp, nil
}

// UnaryInterceptor returns a gRPC unary server interceptor for DPU routing.
// It intercepts unary RPC calls and checks for routing metadata.
func (p *DPUProxy) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract routing metadata
		targetMeta := ExtractTargetMetadata(ctx)

		// If DPU routing is requested, validate and route
		if targetMeta.IsDPUTarget() {
			// Check if this method is forwardable
			if !p.shouldForwardToDPU(info.FullMethod) {
				glog.Warningf("[DPUProxy] Method %s is not forwardable to DPU, rejecting request",
					info.FullMethod)
				return nil, status.Errorf(codes.Unimplemented,
					"method %s does not support DPU routing; remove x-sonic-ss-target-* metadata headers",
					info.FullMethod)
			}

			glog.Infof("[DPUProxy] DPU routing requested: method=%s, target-type=%s, target-index=%s",
				info.FullMethod, targetMeta.TargetType, targetMeta.TargetIndex)

			// Resolve DPU information from Redis (if resolver is available)
			if p.resolver != nil {
				dpuInfo, err := p.resolver.GetDPUInfo(ctx, targetMeta.TargetIndex)
				if err != nil {
					glog.Warningf("[DPUProxy] Error resolving DPU%s: %v, returning error",
						targetMeta.TargetIndex, err)
					return nil, status.Errorf(codes.NotFound,
						"DPU%s not found or unreachable: %v", targetMeta.TargetIndex, err)
				}

				glog.Infof("[DPUProxy] Resolved DPU%s: ip=%s, reachable=%t",
					dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.Reachable)

				if !dpuInfo.Reachable {
					glog.Warningf("[DPUProxy] DPU%s is unreachable, returning error", dpuInfo.Index)
					return nil, status.Errorf(codes.Unavailable,
						"DPU%s is not currently reachable", dpuInfo.Index)
				}

				// Get or create connection to DPU
				conn, err := p.getConnection(ctx, dpuInfo.Index, dpuInfo.IPAddress)
				if err != nil {
					glog.Errorf("[DPUProxy] Failed to get connection to DPU%s: %v", dpuInfo.Index, err)
					return nil, status.Errorf(codes.Internal,
						"failed to connect to DPU%s: %v", dpuInfo.Index, err)
				}

				// Forward the request to DPU based on method
				glog.Infof("[DPUProxy] Forwarding %s to DPU%s at %s:50052",
					info.FullMethod, dpuInfo.Index, dpuInfo.IPAddress)

				// Handle method-specific forwarding
				switch info.FullMethod {
				case "/gnoi.system.System/Time":
					return p.forwardTimeRequest(ctx, conn, req)
				default:
					// This shouldn't happen due to shouldForwardToDPU check, but handle gracefully
					glog.Errorf("[DPUProxy] Unknown forwardable method: %s", info.FullMethod)
					return nil, status.Errorf(codes.Unimplemented,
						"forwarding for method %s not yet implemented", info.FullMethod)
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

		// If DPU routing is requested, validate and route
		if targetMeta.IsDPUTarget() {
			// Check if this method is forwardable
			if !p.shouldForwardToDPU(info.FullMethod) {
				glog.Warningf("[DPUProxy] Method %s is not forwardable to DPU, rejecting stream",
					info.FullMethod)
				return status.Errorf(codes.Unimplemented,
					"method %s does not support DPU routing; remove x-sonic-ss-target-* metadata headers",
					info.FullMethod)
			}

			glog.Infof("[DPUProxy] DPU streaming requested: method=%s, target-type=%s, target-index=%s",
				info.FullMethod, targetMeta.TargetType, targetMeta.TargetIndex)

			// Resolve DPU information from Redis (if resolver is available)
			if p.resolver != nil {
				dpuInfo, err := p.resolver.GetDPUInfo(ctx, targetMeta.TargetIndex)
				if err != nil {
					glog.Warningf("[DPUProxy] Error resolving DPU%s: %v, returning error",
						targetMeta.TargetIndex, err)
					return status.Errorf(codes.NotFound,
						"DPU%s not found or unreachable: %v", targetMeta.TargetIndex, err)
				}

				glog.Infof("[DPUProxy] Resolved DPU%s: ip=%s, reachable=%t",
					dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.Reachable)

				if !dpuInfo.Reachable {
					glog.Warningf("[DPUProxy] DPU%s is unreachable, returning error", dpuInfo.Index)
					return status.Errorf(codes.Unavailable,
						"DPU%s is not currently reachable", dpuInfo.Index)
				}

				// TODO (Phase 2.1): Implement actual DPU streaming proxy
				// 1. Get or create gRPC client connection to dpuInfo.IPAddress:50052
				// 2. Forward the stream to DPU
				// 3. Bidirectionally relay messages between client and DPU
				glog.Infof("[DPUProxy] TODO: Would forward stream %s to %s:50052 (not implemented yet)",
					info.FullMethod, dpuInfo.IPAddress)
			}
		}

		// Pass through to the next handler in the chain
		return handler(srv, ss)
	}
}
