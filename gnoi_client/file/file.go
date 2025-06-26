package file

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	pb "github.com/openconfig/gnoi/file"
	gnoitypes "github.com/openconfig/gnoi/types"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/config"
	"github.com/sonic-net/sonic-gnmi/gnoi_client/utils"
	"google.golang.org/grpc"
)

const chunkSize = 64 * 1024 // 64 KB

func logErrorAndExit(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", a...)
	os.Exit(1)
}

func Stat(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Stat")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)

	req := &pb.StatRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		logErrorAndExit("Failed to parse input JSON: %v", err)
	}
	resp, err := fc.Stat(ctx, req)
	if err != nil {
		logErrorAndExit("Stat RPC failed: %v", err)
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		logErrorAndExit("Failed to marshal StatResponse: %v", err)
	}
	fmt.Println(string(respstr))
}

func Get(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Get")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)

	req := &pb.GetRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		logErrorAndExit("Failed to parse input JSON: %v", err)
	}

	// Start Get stream
	stream, err := fc.Get(ctx, req)
	if err != nil {
		logErrorAndExit("Get RPC failed: %v", err)
	}

	// Determine output path
	outputPath := *config.OutputFile
	if outputPath == "" {
		fileName := filepath.Base(req.GetRemoteFile())
		outputPath = filepath.Join("/tmp", fileName)
	}

	outputFile, err := os.Create(outputPath)
	if err != nil {
		logErrorAndExit("Failed to create output file: %v", err)
	}
	defer outputFile.Close()

	// Receive and write chunks
	for {
		resp, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			logErrorAndExit("Error receiving stream: %v", err)
		}

		switch r := resp.Response.(type) {
		case *pb.GetResponse_Contents:
			_, err := outputFile.Write(r.Contents)
			if err != nil {
				logErrorAndExit("Failed to write to file: %v", err)
			}
		case *pb.GetResponse_Hash:
			fmt.Printf("Received file hash: %v\n", r.Hash)
		default:
			fmt.Println("Unknown GetResponse type")
		}
	}
	fmt.Printf("File successfully written to: %s\n", outputPath)
}

func Put(conn *grpc.ClientConn, ctx context.Context) {
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)

	// Parse JSON input into PutRequest.Details
	var input struct {
		RemoteFile  string `json:"remote_file"`
		Permissions uint32 `json:"permissions"`
	}
	err := json.Unmarshal([]byte(*config.Args), &input)
	if err != nil {
		logErrorAndExit("failed to parse --jsonin: %v", err)
	}

	if *config.InputFile == "" {
		logErrorAndExit("--input_file is required for Put RPC")
	}

	// Open the local file
	f, err := os.Open(*config.InputFile)
	if err != nil {
		logErrorAndExit("failed to open input file %s: %v", *config.InputFile, err)
	}
	defer f.Close()

	stream, err := fc.Put(ctx)
	if err != nil {
		logErrorAndExit("Put RPC failed: %w", err)
	}

	// Send the initial Open message
	err = stream.Send(&pb.PutRequest{
		Request: &pb.PutRequest_Open{
			Open: &pb.PutRequest_Details{
				RemoteFile:  input.RemoteFile,
				Permissions: input.Permissions,
			},
		},
	})
	if err != nil {
		logErrorAndExit("failed to send open message: %v", err)
	}

	// Send file content in chunks
	hasher := sha256.New()
	buf := make([]byte, chunkSize)
	for {
		n, err := f.Read(buf)
		if err != nil && err != io.EOF {
			logErrorAndExit("failed reading input file: %v", err)
		}
		if n == 0 {
			break
		}

		chunk := buf[:n]
		hasher.Write(chunk)

		err = stream.Send(&pb.PutRequest{
			Request: &pb.PutRequest_Contents{
				Contents: chunk,
			},
		})
		if err != nil {
			logErrorAndExit("failed sending chunk: %v", err)
		}
	}

	// Send final hash message
	hasher = sha256.New()
	fileHash := hasher.Sum(nil)
	err = stream.Send(&pb.PutRequest{
		Request: &pb.PutRequest_Hash{
			Hash: &gnoitypes.HashType{
				Method: gnoitypes.HashType_SHA256,
				Hash:   fileHash,
			},
		},
	})
	if err != nil {
		logErrorAndExit("failed sending hash: %v", err)
	}

	// Close and receive final response
	_, err = stream.CloseAndRecv()
	if err != nil {
		logErrorAndExit("Put RPC error on close: %v", err)
	}

	fmt.Printf("Put operation succeeded. Remote file: %s\n", input.RemoteFile)
}

func Remove(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File Remove")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)

	req := &pb.RemoveRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		logErrorAndExit("Failed to parse input JSON: %v", err)
	}
	resp, err := fc.Remove(ctx, req)
	if err != nil {
		logErrorAndExit("Remove RPC failed: %v", err)
	}

	respstr, err := json.Marshal(resp)
	if err != nil {
		logErrorAndExit("Failed to marshal RemoveResponse: %v", err)
	}
	fmt.Println(string(respstr))
}

func TransferToRemote(conn *grpc.ClientConn, ctx context.Context) {
	fmt.Println("File TransferToRemote")
	ctx = utils.SetUserCreds(ctx)
	fc := pb.NewFileClient(conn)

	req := &pb.TransferToRemoteRequest{}
	err := json.Unmarshal([]byte(*config.Args), req)
	if err != nil {
		logErrorAndExit("Failed to parse input JSON: %v", err)
	}
	resp, err := fc.TransferToRemote(ctx, req)
	if err != nil {
		logErrorAndExit("TransferToRemote RPC failed: %v", err)
	}
	respstr, err := json.Marshal(resp)
	if err != nil {
		logErrorAndExit("Failed to marshal TransferToRemoteResponse: %v", err)
	}
	fmt.Println(string(respstr))
}
