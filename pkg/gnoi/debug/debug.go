package debug

import (
	"context"
	"sync"
	"time"

	debug "github.com/sonic-net/sonic-gnmi/internal/debug"
	debug_pb "github.com/sonic-net/sonic-gnmi/proto/gnoi/debug"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// Allow DI for mocking
	runCommand = func(ctx context.Context, outCh chan<- string, errCh chan<- string, cmdStr string, roleAccount string, byteLimit int64) (int, error) {
		return debug.RunCommand(ctx, outCh, errCh, cmdStr, roleAccount, byteLimit)
	}
)

// HandleCommandRequest implements the logic for the Debug RPC, per the gNOI spec.
// It validates the request, then runs the command on the host, streaming responses
// back to the client.
//
// Responses are streamed to the client in the following order:
//   - Request: 1, beginning of execution
//   - Data ([]byte): 0 - many, during execution
//   - Status: 1, upon completion
//
// Returns:
//   - Error with appropriate gRPC status code on failure
func HandleCommandRequest(
	req *debug_pb.DebugRequest,
	stream debug_pb.Debug_DebugServer,
) error {
	ctx := stream.Context()

	// Validate request
	if req == nil {
		return status.Error(codes.InvalidArgument, "request cannot be nil")
	}

	command := req.GetCommand()
	if command == nil {
		return status.Error(codes.InvalidArgument, "command cannot be nil")
	}

	// Optional args
	byteLimit := req.GetByteLimit()
	roleAccount := req.GetRoleAccount()
	timeout := req.GetTimeout()
	if timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout))
		defer cancel()
		ctx = timeoutCtx
	}

	mode := req.GetMode()
	var wg sync.WaitGroup
	outCh := make(chan string, 100)
	errCh := make(chan string, 100)

	switch mode {
	case debug_pb.DebugRequest_MODE_CLI:
		// 1. Send request, indicating start of execution
		err := sendReqInResponse(stream, req)
		if err != nil {
			return nil
		}

		// 2. Send stdout/stderr
		wg.Add(2)
		go func() {
			defer wg.Done()
			streamDataInChannel(ctx, stream, outCh)
		}()
		go func() {
			defer wg.Done()
			streamDataInChannel(ctx, stream, errCh)
		}()

		exitCode, err := runCommand(ctx, outCh, errCh, string(command), roleAccount, byteLimit)
		if err != nil {
			return status.Errorf(codes.FailedPrecondition, "Failed to run command '%s': '%v'", command, err)
		}
		wg.Wait()

		// 3. Send status (with exit code), indicating completion
		sendStatusInResponse(stream, exitCode)

	case debug_pb.DebugRequest_MODE_SHELL:
		return status.Error(codes.Unimplemented, "mode SHELL is currently unimplemented")
	case debug_pb.DebugRequest_MODE_UNSPECIFIED:
		return status.Error(codes.InvalidArgument, "mode cannot be UNSPECIFIED")
	}

	return nil
}

// Helper to send all data held within a channel to stream, in succession.
// If the context completes, or the stream breaks, will abort early.
// Otherwise, will run till the channel is closed by the writer.
func streamDataInChannel(ctx context.Context, stream debug_pb.Debug_DebugServer, ch <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}

			if err := sendDataInResponse(stream, data); err != nil {
				return
			}
		}
	}
}

// Helper functions to hide creation of nested res structs

func sendReqInResponse(stream debug_pb.Debug_DebugServer, req *debug_pb.DebugRequest) error {
	return stream.Send(
		&debug_pb.DebugResponse{
			Response: &debug_pb.DebugResponse_Request{
				Request: req,
			},
		},
	)
}

func sendDataInResponse(stream debug_pb.Debug_DebugServer, data string) error {
	return stream.Send(
		&debug_pb.DebugResponse{
			Response: &debug_pb.DebugResponse_Data{
				Data: []byte(data),
			},
		},
	)
}

func sendStatusInResponse(stream debug_pb.Debug_DebugServer, exitCode int) error {
	return stream.Send(
		&debug_pb.DebugResponse{
			Response: &debug_pb.DebugResponse_Status{
				Status: &debug_pb.DebugStatus{
					Code: int32(exitCode),
				},
			},
		},
	)
}
