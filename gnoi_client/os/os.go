package os

import (
	"context"
	"encoding/json"
	"fmt"
	pb "github.com/openconfig/gnoi/os"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
	"io"
	"os"
)

func logErrorAndExit(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func Verify(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("OS Verify")
	ctx = utils.SetUserCreds(ctx)
	osc := pb.NewOSClient(conn)
	resp, err := osc.Verify(ctx, new(pb.VerifyRequest))
	if err != nil {
		panic(err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic(err.Error())
	}
	fmt.Println(string(respstr))
}

func Activate(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("OS Activate")
	ctx = utils.SetUserCreds(ctx)
	osc := pb.NewOSClient(conn)
	req := &pb.ActivateRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		panic("Failed to unmarshal JSON: " + err.Error())
	}
	resp, err := osc.Activate(ctx, req)
	if err != nil {
		panic("Failed to activate OS." + err.Error())
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		panic("Failed to marshal reponse: " + err.Error())
	}
	fmt.Println(string(respstr))
}

func Install(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("OS Install")
	ctx = utils.SetUserCreds(ctx)
	osc := pb.NewOSClient(conn)

	// Parse input args as generic map to extract image path separately
	var input struct {
		TransferRequest *pb.TransferRequest `json:"transferRequest,omitempty"`
	}
	err := json.Unmarshal([]byte(*config.Args), &input)
	if err != nil {
		logErrorAndExit("Failed to parse input JSON: %v", err)
	}
	if input.TransferRequest == nil {
		logErrorAndExit("Missing 'transferRequest' field in input JSON")
	}
	if config.InputFile == nil || *config.InputFile == "" {
		logErrorAndExit("--input_file is required for Install RPC")
	}

	// Start Install stream
	stream, err := osc.Install(ctx)
	if err != nil {
		logErrorAndExit("Failed to start Install stream: %v", err)
	}

	// Step 1: Send TransferRequest
	if err := stream.Send(&pb.InstallRequest{
		Request: &pb.InstallRequest_TransferRequest{
			TransferRequest: input.TransferRequest,
		},
	}); err != nil {
		logErrorAndExit("Failed to send TransferRequest: %v", err)
	}

	// Step 2: Wait for InstallResponse
waitLoop:
	for {
		resp, err := stream.Recv()
		if err != nil {
			logErrorAndExit("Error receiving stream: %v", err)
		}
		if resp == nil {
			logErrorAndExit("Install RPC failed: %v", err)
		}
		respstr, err := json.Marshal(resp)
		if err != nil {
			logErrorAndExit("Failed to marshal InstallResponse: %v", err)
		}
		fmt.Println(string(respstr))

		fmt.Println("Processing Install Response in Client!")
		switch r := resp.Response.(type) {
		case *pb.InstallResponse_Validated:
			fmt.Printf("OS already installed and validated: version=%s\n", r.Validated.Version)
			return
		case *pb.InstallResponse_TransferReady:
			fmt.Println("Target ready to receive OS image.")
			break waitLoop
		case *pb.InstallResponse_SyncProgress:
			fmt.Printf("SyncProgress: %d%% transferred\n", r.SyncProgress.PercentageTransferred)
		case *pb.InstallResponse_InstallError:
			logErrorAndExit("InstallError from client: %v - %s", r.InstallError.Type, r.InstallError.Detail)
		default:
			fmt.Printf("Unexpected initial InstallResponse: %T\n", r)
		}
	}

	// Step 3: Concurrently drain InstallResponses while streaming content.
	//
	// The target sends TransferProgress messages as it writes the image. If the
	// client only sends and never receives, the HTTP/2 flow-control windows fill
	// in both directions and the transfer deadlocks (the target blocks writing
	// progress, so it stops reading content, so the client blocks sending). We
	// therefore read responses in a dedicated goroutine for the whole transfer.
	recvDone := make(chan error, 1)
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				recvDone <- nil
				return
			}
			if err != nil {
				recvDone <- fmt.Errorf("error receiving InstallResponse: %v", err)
				return
			}
			switch r := resp.Response.(type) {
			case *pb.InstallResponse_Validated:
				fmt.Printf("OS installation successful. Version: %s\n", r.Validated.Version)
				recvDone <- nil
				return
			case *pb.InstallResponse_TransferProgress:
				fmt.Printf("Transfer Progress: %d bytes\n", r.TransferProgress.BytesReceived)
			case *pb.InstallResponse_InstallError:
				recvDone <- fmt.Errorf("InstallError: %v - %s", r.InstallError.Type, r.InstallError.Detail)
				return
			default:
				fmt.Printf("Other InstallResponse: %T\n", r)
			}
		}
	}()

	// Open the input file and stream transfer_content chunks.
	file, err := os.Open(*config.InputFile)
	if err != nil {
		logErrorAndExit("Failed to open OS image file %s: %v", *config.InputFile, err)
	}
	defer file.Close()

	var sendErr error
	buffer := make([]byte, 1<<20) // 1 MiB chunks
	for {
		n, rerr := file.Read(buffer)
		if n > 0 {
			if serr := stream.Send(&pb.InstallRequest{
				Request: &pb.InstallRequest_TransferContent{
					TransferContent: buffer[:n],
				},
			}); serr != nil {
				// The target likely aborted the stream (e.g. InstallError).
				// Stop sending and let the receiver goroutine report the real
				// error instead of the generic send failure.
				sendErr = serr
				break
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			logErrorAndExit("Failed to read OS file: %v", rerr)
		}
	}
	if sendErr == nil {
		fmt.Println("Finished sending OS image.")

		// Step 4: Send TransferEnd, then half-close the send direction.
		if err := stream.Send(&pb.InstallRequest{
			Request: &pb.InstallRequest_TransferEnd{
				TransferEnd: &pb.TransferEnd{},
			},
		}); err != nil {
			sendErr = err
		} else {
			fmt.Println("Sent TransferEnd")
		}
	}
	if err := stream.CloseSend(); err != nil {
		fmt.Printf("Warning: CloseSend failed: %v\n", err)
	}

	// Step 5: Wait for the receiver goroutine to observe Validated / InstallError / EOF.
	if rerr := <-recvDone; rerr != nil {
		logErrorAndExit("%v", rerr)
	}
}
