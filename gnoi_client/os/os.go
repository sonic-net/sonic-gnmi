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

	// Step 3: Send transfer_content chunks from the image file
	// Open the input file
	file, err := os.Open(*config.InputFile)
	if err != nil {
		logErrorAndExit("Failed to open OS image file %s: %v", *config.InputFile, err)
	}
	defer file.Close()

	buffer := make([]byte, 4096)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			err = stream.Send(&pb.InstallRequest{
				Request: &pb.InstallRequest_TransferContent{
					TransferContent: buffer[:n],
				},
			})
			if err != nil {
				logErrorAndExit("Failed to send transfer_content: %v (n=%d)", err, n)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			logErrorAndExit("Failed to read OS file: %v", err)
		}
	}
	fmt.Println("Finished sending OS image.")

	// Step 4: Send TransferEnd
	if err := stream.Send(&pb.InstallRequest{
		Request: &pb.InstallRequest_TransferEnd{
			TransferEnd: &pb.TransferEnd{},
		},
	}); err != nil {
		logErrorAndExit("Failed to send TransferEnd: %v", err)
	}
	fmt.Println("Sent TransferEnd")

	// Step 5: Wait for final validation or error
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logErrorAndExit("Error receiving final InstallResponse: %v", err)
		}

		switch r := resp.Response.(type) {
		case *pb.InstallResponse_Validated:
			fmt.Printf("OS installation successful. Version: %s\n", r.Validated.Version)
			return
		case *pb.InstallResponse_TransferProgress:
			fmt.Printf("Transfer Progress: %d bytes\n", r.TransferProgress.BytesReceived)
		case *pb.InstallResponse_InstallError:
			logErrorAndExit("InstallError: %v - %s", r.InstallError.Type, r.InstallError.Detail)
		default:
			fmt.Printf("Other InstallResponse: %T\n", r)
		}
	}
}
