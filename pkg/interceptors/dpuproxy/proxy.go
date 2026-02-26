package dpuproxy

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/golang/glog"
	gnoi_file_pb "github.com/openconfig/gnoi/file"
	system "github.com/openconfig/gnoi/system"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

// ForwardingMode defines how a method should be handled when DPU headers are present.
type ForwardingMode string

const (
	// ForwardToDPU sends the request directly to the specified DPU
	ForwardToDPU ForwardingMode = "forward"
	// HandleLocally processes the request locally but preserves DPU context for special logic
	HandleLocally ForwardingMode = "local"
)

// ForwardableMethod represents a gRPC method that can be processed when DPU headers are present.
type ForwardableMethod struct {
	FullMethod  string
	Description string
	Mode        ForwardingMode
}

// defaultForwardableMethods is the registry of methods that can be processed when DPU headers are present.
// Methods not in this registry will be rejected with an error when DPU headers are provided.
var defaultForwardableMethods = []ForwardableMethod{
	{
		FullMethod:  "/gnoi.system.System/Time",
		Description: "Get current time from DPU (for testing)",
		Mode:        ForwardToDPU,
	},
	{
		FullMethod:  "/gnoi.file.File/Put",
		Description: "Upload file to DPU",
		Mode:        ForwardToDPU,
	},
	{
		FullMethod:  "/gnoi.file.File/TransferToRemote",
		Description: "Download from URL, then upload to DPU",
		Mode:        HandleLocally,
	},
	{
		FullMethod:  "/gnoi.system.System/Reboot",
		Description: "Reboot DPU from NPU host",
		Mode:        HandleLocally,
	},
	{
		FullMethod:  "/gnoi.system.System/SetPackage",
		Description: "Install package on DPU",
		Mode:        ForwardToDPU,
	},
	// gRPC reflection methods needed for grpcurl to work with DPU headers
	{
		FullMethod:  "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		Description: "gRPC reflection service v1",
		Mode:        HandleLocally,
	},
	{
		FullMethod:  "/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
		Description: "gRPC reflection service v1alpha",
		Mode:        HandleLocally,
	},
}

var defaultProxy *DPUProxy

// SetDefaultProxy registers the DPU proxy singleton for use by handlers
// that need direct DPU connections (e.g., TransferToRemote file operations).
func SetDefaultProxy(p *DPUProxy) { defaultProxy = p }

// GetDPUConnection returns a cached gRPC connection to the specified DPU.
// It resolves DPU info via Redis and establishes/reuses connections.
// The returned connection is cached — callers must NOT close it.
func GetDPUConnection(ctx context.Context, dpuIndex string) (*grpc.ClientConn, error) {
	if defaultProxy == nil {
		return nil, fmt.Errorf("DPU proxy not initialized")
	}
	return defaultProxy.GetDPUConnection(ctx, dpuIndex)
}

// DPUProxy is a gRPC interceptor that routes requests to DPU targets based on metadata.
// It examines incoming gRPC metadata for x-sonic-ss-target-type and x-sonic-ss-target-index
// headers and routes requests accordingly.
//
// Current implementation (Phase 2.1): Actual forwarding to DPU gRPC servers with connection management.
type DPUProxy struct {
	resolver *DPUResolver

	// Connection management
	connMu    sync.RWMutex
	conns     map[string]*grpc.ClientConn // key: DPU index (e.g., "0", "1")
	connPorts map[string]string           // key: DPU index, value: successful port

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
		resolver:  resolver,
		conns:     make(map[string]*grpc.ClientConn),
		connPorts: make(map[string]string),
	}
}

// GetDPUConnection resolves DPU info and returns a cached gRPC connection to the DPU.
// The returned connection is cached — callers must NOT close it.
func (p *DPUProxy) GetDPUConnection(ctx context.Context, dpuIndex string) (*grpc.ClientConn, error) {
	if p.resolver == nil {
		return nil, fmt.Errorf("resolver not available")
	}
	dpuInfo, err := p.resolver.GetDPUInfo(ctx, dpuIndex)
	if err != nil {
		return nil, err
	}
	if !dpuInfo.Reachable {
		return nil, status.Errorf(codes.Unavailable, "DPU%s not reachable", dpuIndex)
	}
	return p.getConnection(ctx, dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.GNMIPortsToTry)
}

// getForwardingMode checks if a method is registered and returns its forwarding mode.
// Returns the ForwardingMode and a boolean indicating if the method was found.
func (p *DPUProxy) getForwardingMode(method string) (ForwardingMode, bool) {
	for _, m := range defaultForwardableMethods {
		if m.FullMethod == method {
			return m.Mode, true
		}
	}
	return "", false
}

// getConnection retrieves or creates a gRPC connection to the specified DPU.
// Connections are cached and reused. Uses keepalive settings for long-lived connections.
func (p *DPUProxy) getConnection(ctx context.Context, dpuIndex, ipAddress string, portsToTry []string) (*grpc.ClientConn, error) {
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

	// Try multiple ports to find the working gNMI service
	var lastErr error
	for i, port := range portsToTry {
		target := fmt.Sprintf("%s:%s", ipAddress, port)
		glog.Infof("[DPUProxy] Trying to connect to DPU%s at %s (attempt %d/%d)", dpuIndex, target, i+1, len(portsToTry))

		// Create connection with keepalive settings for long-lived connections
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
			lastErr = err
			glog.Warningf("[DPUProxy] Failed to connect to DPU%s at %s: %v", dpuIndex, target, err)
			continue
		}

		// Test the connection with a simple health check RPC
		// Use a short timeout for the connectivity test
		testCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		// Try a simple gNOI System.Time RPC as a connectivity test
		systemClient := system.NewSystemClient(conn)
		_, err = systemClient.Time(testCtx, &system.TimeRequest{})

		// If the RPC succeeds OR fails with a known gRPC error (not network error),
		// we consider the connection working. Network errors indicate unreachable target.
		if err == nil || status.Code(err) != codes.Unavailable {
			// Cache the successful connection and port for reuse
			p.conns[dpuIndex] = conn
			p.connPorts[dpuIndex] = port
			glog.Infof("[DPUProxy] Successfully verified connectivity to DPU%s at %s", dpuIndex, target)
			return conn, nil
		}

		// Connection test failed, try next port
		conn.Close()
		lastErr = err
		glog.Warningf("[DPUProxy] Connectivity test failed for DPU%s at %s: %v", dpuIndex, target, err)
	}

	return nil, fmt.Errorf("failed to connect to DPU%s on any port %v: last error: %w", dpuIndex, portsToTry, lastErr)
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

// forwardStream forwards a streaming RPC to the DPU.
// This implements bidirectional streaming proxy between client and DPU.
func (p *DPUProxy) forwardStream(ctx context.Context, conn *grpc.ClientConn, ss grpc.ServerStream, info *grpc.StreamServerInfo) error {
	// For File.Put, we need to handle the streaming RPC
	if info.FullMethod == "/gnoi.file.File/Put" {
		return p.forwardFilePutStream(ctx, conn, ss)
	}

	// For System.SetPackage, we need to handle the streaming RPC
	if info.FullMethod == "/gnoi.system.System/SetPackage" {
		return p.forwardSetPackageStream(ctx, conn, ss)
	}

	// Add other stream methods here as needed
	return status.Errorf(codes.Unimplemented, "stream forwarding for method %s not implemented", info.FullMethod)
}

// forwardFilePutStream forwards a File.Put streaming RPC to the DPU.
func (p *DPUProxy) forwardFilePutStream(ctx context.Context, conn *grpc.ClientConn, ss grpc.ServerStream) error {
	// Create File client for DPU
	fileClient := gnoi_file_pb.NewFileClient(conn)

	// Create a client stream to the DPU
	clientStream, err := fileClient.Put(ctx)
	if err != nil {
		glog.Errorf("[DPUProxy] Failed to create client stream to DPU: %v", err)
		return status.Errorf(codes.Internal, "failed to create client stream to DPU: %v", err)
	}

	// Channel to signal completion of request forwarding
	forwardDone := make(chan error, 1)

	// Goroutine to forward requests from client to DPU
	go func() {
		defer func() {
			// Close the send side when done receiving from client
			if err := clientStream.CloseSend(); err != nil {
				glog.Warningf("[DPUProxy] Error closing send on client stream: %v", err)
			}
		}()

		for {
			// Receive request from client
			var req gnoi_file_pb.PutRequest
			if err := ss.RecvMsg(&req); err != nil {
				if err == io.EOF {
					glog.V(2).Infof("[DPUProxy] Client finished sending requests")
					forwardDone <- nil
					return
				}
				glog.Errorf("[DPUProxy] Error receiving from client: %v", err)
				forwardDone <- err
				return
			}

			// Forward request to DPU
			if err := clientStream.Send(&req); err != nil {
				glog.Errorf("[DPUProxy] Error sending to DPU: %v", err)
				forwardDone <- err
				return
			}
		}
	}()

	// Wait for all requests to be forwarded
	if err := <-forwardDone; err != nil {
		return err
	}

	// Now get the response from DPU after all requests are sent
	response, err := clientStream.CloseAndRecv()
	if err != nil {
		glog.Errorf("[DPUProxy] Error getting response from DPU: %v", err)
		return status.Errorf(codes.Internal, "error getting response from DPU: %v", err)
	}

	// Send response back to client
	if err := ss.SendMsg(response); err != nil {
		glog.Errorf("[DPUProxy] Error sending response to client: %v", err)
		return status.Errorf(codes.Internal, "error sending response to client: %v", err)
	}

	glog.Infof("[DPUProxy] Successfully forwarded File.Put stream to DPU")
	return nil
}

// forwardSetPackageStream forwards a System.SetPackage streaming RPC to the DPU.
func (p *DPUProxy) forwardSetPackageStream(ctx context.Context, conn *grpc.ClientConn, ss grpc.ServerStream) error {
	// Create System client for DPU
	systemClient := system.NewSystemClient(conn)

	// Create a client stream to the DPU
	clientStream, err := systemClient.SetPackage(ctx)
	if err != nil {
		glog.Errorf("[DPUProxy] Failed to create SetPackage client stream to DPU: %v", err)
		return status.Errorf(codes.Internal, "failed to create SetPackage client stream to DPU: %v", err)
	}

	// Channel to signal completion of request forwarding
	forwardDone := make(chan error, 1)

	// Goroutine to forward requests from client to DPU
	go func() {
		defer func() {
			// Close the send side when done receiving from client
			if err := clientStream.CloseSend(); err != nil {
				glog.Warningf("[DPUProxy] Error closing send on SetPackage client stream: %v", err)
			}
		}()

		for {
			// Receive request from client
			var req system.SetPackageRequest
			if err := ss.RecvMsg(&req); err != nil {
				if err == io.EOF {
					glog.V(2).Infof("[DPUProxy] SetPackage client finished sending requests")
					forwardDone <- nil
					return
				}
				glog.Errorf("[DPUProxy] Error receiving SetPackage from client: %v", err)
				forwardDone <- err
				return
			}

			// Forward request to DPU
			if err := clientStream.Send(&req); err != nil {
				glog.Errorf("[DPUProxy] Error sending SetPackage to DPU: %v", err)
				forwardDone <- err
				return
			}
		}
	}()

	// Wait for all requests to be forwarded
	if err := <-forwardDone; err != nil {
		return err
	}

	// Now get the response from DPU after all requests are sent
	response, err := clientStream.CloseAndRecv()
	if err != nil {
		glog.Errorf("[DPUProxy] Error getting SetPackage response from DPU: %v", err)
		return status.Errorf(codes.Internal, "error getting SetPackage response from DPU: %v", err)
	}

	// Send response back to client
	if err := ss.SendMsg(response); err != nil {
		glog.Errorf("[DPUProxy] Error sending SetPackage response to client: %v", err)
		return status.Errorf(codes.Internal, "error sending SetPackage response to client: %v", err)
	}

	glog.Infof("[DPUProxy] Successfully forwarded System.SetPackage stream to DPU")
	return nil
}

// UnaryInterceptor returns a gRPC unary server interceptor for DPU routing.
// It intercepts unary RPC calls and checks for routing metadata.
func (p *DPUProxy) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract routing metadata
		targetMeta := ExtractTargetMetadata(ctx)

		// If DPU routing is requested, validate and route
		if targetMeta.IsDPUTarget() {
			// Check forwarding mode for this method
			mode, found := p.getForwardingMode(info.FullMethod)
			if !found {
				glog.Warningf("[DPUProxy] Method %s is not registered for DPU routing, rejecting request",
					info.FullMethod)
				return nil, status.Errorf(codes.Unimplemented,
					"method %s does not support DPU routing; remove x-sonic-ss-target-* metadata headers",
					info.FullMethod)
			}

			glog.Infof("[DPUProxy] DPU routing requested: method=%s, mode=%s, target-type=%s, target-index=%s",
				info.FullMethod, mode, targetMeta.TargetType, targetMeta.TargetIndex)

			// Handle based on forwarding mode
			switch mode {
			case HandleLocally:
				// Pass through to NPU handler but preserve DPU context in metadata
				glog.Infof("[DPUProxy] HandleLocally mode: passing %s to local handler with DPU context preserved",
					info.FullMethod)
				// The DPU context is already in ctx metadata, NPU handler can access it
				return handler(ctx, req)

			case ForwardToDPU:
				// Forward to DPU - existing logic
				glog.Infof("[DPUProxy] ForwardToDPU mode: routing %s to DPU", info.FullMethod)

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
					conn, err := p.getConnection(ctx, dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.GNMIPortsToTry)
					if err != nil {
						glog.Errorf("[DPUProxy] Failed to get connection to DPU%s: %v", dpuInfo.Index, err)
						return nil, status.Errorf(codes.Internal,
							"failed to connect to DPU%s: %v", dpuInfo.Index, err)
					}

					// Forward the request to DPU based on method
					actualPort := p.connPorts[dpuInfo.Index]
					glog.Infof("[DPUProxy] Forwarding %s to DPU%s at %s:%s",
						info.FullMethod, dpuInfo.Index, dpuInfo.IPAddress, actualPort)

					// Handle method-specific forwarding
					switch info.FullMethod {
					case "/gnoi.system.System/Time":
						return p.forwardTimeRequest(ctx, conn, req)
					default:
						// This shouldn't happen due to getForwardingMode check, but handle gracefully
						glog.Errorf("[DPUProxy] Unknown forwardable method: %s", info.FullMethod)
						return nil, status.Errorf(codes.Unimplemented,
							"forwarding for method %s not yet implemented", info.FullMethod)
					}
				}

			default:
				glog.Errorf("[DPUProxy] Unknown forwarding mode %s for method %s", mode, info.FullMethod)
				return nil, status.Errorf(codes.Internal,
					"unknown forwarding mode for method %s", info.FullMethod)
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
			// Check forwarding mode for this method
			mode, found := p.getForwardingMode(info.FullMethod)
			if !found {
				glog.Warningf("[DPUProxy] Method %s is not registered for DPU routing, rejecting stream",
					info.FullMethod)
				return status.Errorf(codes.Unimplemented,
					"method %s does not support DPU routing; remove x-sonic-ss-target-* metadata headers",
					info.FullMethod)
			}

			glog.Infof("[DPUProxy] DPU streaming requested: method=%s, mode=%s, target-type=%s, target-index=%s",
				info.FullMethod, mode, targetMeta.TargetType, targetMeta.TargetIndex)

			// Handle based on forwarding mode
			switch mode {
			case HandleLocally:
				// Pass through to NPU handler but preserve DPU context in metadata
				glog.Infof("[DPUProxy] HandleLocally mode: passing stream %s to local handler with DPU context preserved",
					info.FullMethod)
				// The DPU context is already in ctx metadata, NPU handler can access it
				return handler(srv, ss)

			case ForwardToDPU:
				// Forward to DPU - existing logic
				glog.Infof("[DPUProxy] ForwardToDPU mode: routing stream %s to DPU", info.FullMethod)

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

					// Get or create connection to DPU
					conn, err := p.getConnection(ctx, dpuInfo.Index, dpuInfo.IPAddress, dpuInfo.GNMIPortsToTry)
					if err != nil {
						glog.Errorf("[DPUProxy] Failed to get connection to DPU%s: %v", dpuInfo.Index, err)
						return status.Errorf(codes.Internal,
							"failed to connect to DPU%s: %v", dpuInfo.Index, err)
					}

					// Forward the stream to DPU
					actualPort := p.connPorts[dpuInfo.Index]
					glog.Infof("[DPUProxy] Forwarding stream %s to DPU%s at %s:%s",
						info.FullMethod, dpuInfo.Index, dpuInfo.IPAddress, actualPort)

					return p.forwardStream(ctx, conn, ss, info)
				}

			default:
				glog.Errorf("[DPUProxy] Unknown forwarding mode %s for stream method %s", mode, info.FullMethod)
				return status.Errorf(codes.Internal,
					"unknown forwarding mode for stream method %s", info.FullMethod)
			}
		}

		// Pass through to the next handler in the chain
		return handler(srv, ss)
	}
}
