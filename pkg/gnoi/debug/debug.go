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
	ctx context.Context,
	req *debug_pb.DebugRequest,
	stream debug_pb.Debug_DebugServer,
) error {
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
	done := make(chan bool, 1)
	outCh := make(chan string, 100)
	errCh := make(chan string, 100)

	switch mode {
	case debug_pb.DebugRequest_MODE_CLI:
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Send request, indicating start of execution
			err := sendReqInResponse(stream, req)
			if err != nil {
				return
			}

			for {
				select {
				// If ctx is done, we can't send any more, end early
				case <-ctx.Done():
					return
				case stdout := <-outCh:
					err := sendDataInResponse(stream, stdout)
					if err != nil {
						// Stream is broken, end early
						return
					}
				case stderr := <-errCh:
					err := sendDataInResponse(stream, stderr)
					if err != nil {
						// Stream is broken, end early
						return
					}
				// Done comes last to ensure outCh & errCh are first empty
				case <-done:
					return
				}
			}
		}()
		exitCode, err := debug.RunCommand(ctx, outCh, errCh, string(command), roleAccount, byteLimit)
		done <- true
		if err != nil {
			return status.Errorf(codes.FailedPrecondition, "Failed to run command '%s': '%v'", command, err)
		}
		wg.Wait()

		// Send status (with exit code), indicating completion
		sendStatusInResponse(stream, exitCode)

	case debug_pb.DebugRequest_MODE_SHELL:
		return status.Error(codes.Unimplemented, "mode SHELL is currently unimplemented")
	case debug_pb.DebugRequest_MODE_UNSPECIFIED:
		return status.Error(codes.InvalidArgument, "mode cannot be UNSPECIFIED")
	}

	return nil
}

// Helper functions to hide creation of nested res structs

func sendReqInResponse(stream debug_pb.Debug_DebugServer, req *debug_pb.DebugRequest) error {
	err := stream.Send(
		&debug_pb.DebugResponse{
			Response: &debug_pb.DebugResponse_Request{
				Request: req,
			},
		},
	)

	return err
}

func sendDataInResponse(stream debug_pb.Debug_DebugServer, data string) error {
	err := stream.Send(
		&debug_pb.DebugResponse{
			Response: &debug_pb.DebugResponse_Data{
				Data: []byte(data),
			},
		},
	)

	return err
}

func sendStatusInResponse(stream debug_pb.Debug_DebugServer, exitCode int) error {
	err := stream.Send(
		&debug_pb.DebugResponse{
			Response: &debug_pb.DebugResponse_Status{
				Status: &debug_pb.DebugStatus{
					Code: int32(exitCode),
				},
			},
		},
	)

	return err
}
