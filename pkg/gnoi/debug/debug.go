package debug

import (
	"context"
	"errors"

	debugpb "github.com/openconfig/gnoi/debug"
	exec "github.com/sonic-net/sonic-gnmi/internal/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var runCommand = exec.RunCommand

type commandResult struct {
	exitCode int
	err      error
}

func HandleCommandRequest(req *debugpb.DebugRequest, stream debugpb.Debug_DebugServer, policy *Policy, access AccessLevel) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	if len(req.GetCommand()) == 0 {
		return status.Error(codes.InvalidArgument, "command is required")
	}
	switch req.GetMode() {
	case debugpb.DebugRequest_MODE_CLI:
	case debugpb.DebugRequest_MODE_SHELL:
		return status.Error(codes.Unimplemented, "shell mode is not supported")
	default:
		return status.Error(codes.InvalidArgument, "CLI mode is required")
	}
	if req.GetRoleAccount() != "" {
		return status.Error(codes.InvalidArgument, "role_account is not supported")
	}

	plan, err := policy.Resolve(string(req.GetCommand()), access, req.GetTimeout(), req.GetByteLimit())
	switch {
	case errors.Is(err, ErrPolicyUnavailable):
		return status.Error(codes.FailedPrecondition, "Debug command policy is unavailable")
	case errors.Is(err, ErrInvalidRequest):
		return status.Error(codes.InvalidArgument, "invalid Debug request")
	case errors.Is(err, ErrRejected):
		return status.Error(codes.PermissionDenied, "Debug action is not permitted")
	case err != nil:
		return status.Error(codes.Internal, "failed to resolve Debug action")
	}

	if err := sendReqInResponse(stream, req); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(stream.Context())
	defer cancel()
	outCh := make(chan string)
	errCh := make(chan string)
	resultCh := make(chan commandResult, 1)
	go func() {
		exitCode, err := runCommand(ctx, outCh, errCh, plan)
		resultCh <- commandResult{exitCode: exitCode, err: err}
	}()

	var sendErr error
	for outCh != nil || errCh != nil {
		select {
		case data, ok := <-outCh:
			if !ok {
				outCh = nil
				continue
			}
			if sendErr == nil {
				sendErr = sendDataInResponse(stream, data)
				if sendErr != nil {
					cancel()
				}
			}
		case data, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if sendErr == nil {
				sendErr = sendDataInResponse(stream, data)
				if sendErr != nil {
					cancel()
				}
			}
		}
	}

	result := <-resultCh
	if sendErr != nil {
		return sendErr
	}
	if result.err != nil {
		return status.Error(codes.FailedPrecondition, "failed to execute Debug action")
	}
	return sendStatusInResponse(stream, int32(result.exitCode))
}

func sendReqInResponse(stream debugpb.Debug_DebugServer, req *debugpb.DebugRequest) error {
	return stream.Send(&debugpb.DebugResponse{
		Response: &debugpb.DebugResponse_Request{Request: req},
	})
}

func sendDataInResponse(stream debugpb.Debug_DebugServer, data string) error {
	return stream.Send(&debugpb.DebugResponse{
		Response: &debugpb.DebugResponse_Data{Data: []byte(data)},
	})
}

func sendStatusInResponse(stream debugpb.Debug_DebugServer, exitCode int32) error {
	return stream.Send(&debugpb.DebugResponse{
		Response: &debugpb.DebugResponse_Status{
			Status: &debugpb.DebugStatus{Code: exitCode},
		},
	})
}
