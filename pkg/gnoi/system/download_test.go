package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/openconfig/gnoi/common"
	syspb "github.com/openconfig/gnoi/system"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/pkg/exec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSetPackageRemoteDownload(t *testing.T) {
	for _, failDownload := range []bool{false, true} {
		t.Run(map[bool]string{false: "install verified staged bytes", true: "no install on download failure"}[failDownload], func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()
			root := t.TempDir()
			dir := filepath.Join(root, "staging")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			patches.ApplyFunc(os.MkdirTemp, func(string, string) (string, error) { return dir, nil })
			var calls []string
			patches.ApplyFunc(download.DownloadTrusted, func(ctx context.Context, rawURL, policy, dst string, limit int64) error {
				calls = append(calls, "download")
				if rawURL != "http://images.example/image" || policy != download.TrustedPolicyPath {
					t.Fatalf("unexpected download: %s, %s", rawURL, policy)
				}
				if failDownload {
					return errors.New("policy rejected")
				}
				return os.WriteFile(dst, []byte("complete image"), 0600)
			})
			patches.ApplyFunc(exec.RunHostCommand, func(ctx context.Context, cmd string, args []string, opts *exec.RunHostCommandOptions) (*exec.CommandResult, error) {
				calls = append(calls, args[0])
				if args[0] == "binary-version" || args[0] == "install" {
					path := args[len(args)-1]
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "complete image" || filepath.Dir(path) != dir {
						t.Fatalf("installer input = %s, %q, %v", path, data, err)
					}
				}
				return &exec.CommandResult{Stdout: "SONiC-OS-test\n"}, nil
			})
			_, err := HandleSetPackage(context.Background(), &syspb.SetPackageRequest{
				Request: &syspb.SetPackageRequest_Package{Package: &syspb.Package{
					Filename: "/var/tmp/requested-image", Activate: true,
					RemoteDownload: &common.RemoteDownload{Path: "http://images.example/image"},
				}},
			})
			want := []string{"download", "binary-version", "install", "set-default"}
			if failDownload {
				want = []string{"download"}
				if status.Code(err) != codes.FailedPrecondition {
					t.Fatalf("error = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("operations = %v, want %v", calls, want)
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatal("staging directory not cleaned")
			}
		})
	}
}

func TestValidateRemoteDownload(t *testing.T) {
	for _, remote := range []*common.RemoteDownload{
		{Path: "file:///image"}, {Path: "http://images.example", Protocol: common.RemoteDownload_SFTP},
		{Path: "http://images.example", SourceVrf: "other"},
		{Path: "http://images.example", SourceAddress: "192.0.2.1"},
	} {
		if status.Code(validateRemoteDownload(remote)) != codes.InvalidArgument {
			t.Fatalf("accepted unsupported remote metadata: %v", remote)
		}
	}
	for _, remote := range []*common.RemoteDownload{
		{Path: "http://images.example", Protocol: common.RemoteDownload_HTTP},
		{Path: "https://images.example", Protocol: common.RemoteDownload_HTTPS},
		{Path: "https://images.example"},
	} {
		if err := validateRemoteDownload(remote); err != nil {
			t.Fatal(err)
		}
	}
}
