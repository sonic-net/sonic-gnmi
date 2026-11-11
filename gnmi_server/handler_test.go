package gnmi

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	pb "github.com/openconfig/gnmi/proto/gnmi"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestNewGNMIHandler(t *testing.T) {
	// Occupied transport endpoints must not be opened, removed or replaced.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	socket := filepath.Join(t.TempDir(), "existing.sock")
	if err := os.WriteFile(socket, []byte("owned by caller"), 0600); err != nil {
		t.Fatal(err)
	}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(newFileServer, func(*Config) *FileServer { t.Fatal("constructed File service"); return nil })
	patches.ApplyFunc(NewGNSIAuthzServer, func(*Server) *GNSIAuthzServer { t.Fatal("constructed Authz"); return nil })
	patches.ApplyFunc(NewGNSIPathzServer, func(*Server) *GNSIPathzServer { t.Fatal("constructed Pathz"); return nil })
	patches.ApplyFunc(NewGNSICertzServer, func(*Server) *GNSICertzServer { t.Fatal("constructed Certz"); return nil })
	patches.ApplyFunc(NewGNSICredentialzServer, func(*Server) *GNSICredentialzServer { t.Fatal("constructed Credentialz"); return nil })
	patches.ApplyFunc(NewDbJournal, func(string) (*DbJournal, error) { t.Fatal("constructed DB journal"); return nil, nil })
	oldJournal := *enableConfigDbJournal
	*enableConfigDbJournal = true
	t.Cleanup(func() { *enableConfigDbJournal = oldJournal })
	cfg := &Config{Port: int64(listener.Addr().(*net.TCPAddr).Port), UnixSocket: socket}
	s, err := NewGNMIHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s.s != nil || s.udsServer != nil || s.lis != nil || s.udsListener != nil || s.Address() != "" {
		t.Fatal("handler owns a transport")
	}
	if s.gnsiAuthz != nil || s.gnsiPathz != nil || s.gnsiCertz != nil || s.gnsiCredentialz != nil || s.configDbJournal != nil {
		t.Fatal("handler constructed an unrelated service")
	}
	if s.clients == nil || s.SaveStartupConfig == nil || s.ReqFromMaster == nil {
		t.Fatal("handler state is incomplete")
	}
	if err := s.SaveStartupConfig(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set(context.Background(), &pb.SetRequest{}); status.Code(err) != codes.Unimplemented {
		t.Fatalf("read-only Set = %v", err)
	}
	if err := s.Serve(); err == nil {
		t.Fatal("handler unexpectedly serves a transport")
	}
	s.Stop()
	s.ForceStop()
	s.Cleanup()
	if data, err := os.ReadFile(socket); err != nil || string(data) != "owned by caller" {
		t.Fatalf("handler Stop changed caller's socket path: %q, %v", data, err)
	}
}

func TestNewGNMIHandlerRejectsUnsupportedPolicy(t *testing.T) {
	for _, cfg := range []*Config{nil, {PathzPolicy: true}, {AuthzPolicy: true}} {
		if s, err := NewGNMIHandler(cfg); err == nil || s != nil {
			t.Fatalf("NewGNMIHandler(%+v) = %v, %v; want rejection", cfg, s, err)
		}
	}
	t.Setenv("SONIC_GNMI_SHM_KEY", "invalid")
	if s, err := NewGNMIHandler(&Config{}); err == nil || s != nil {
		t.Fatalf("invalid shared memory configuration accepted: %v, %v", s, err)
	}
}

// Host the original *Server directly; no forwarding or per-RPC wrappers.
func handlerClient(t *testing.T, cfg *Config) (*Server, pb.GNMIClient) {
	t.Helper()
	s, err := NewGNMIHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(1024 * 1024)
	host := grpc.NewServer()
	pb.RegisterGNMIServer(host, s)
	if services := host.GetServiceInfo(); len(services) != 1 {
		t.Fatalf("unexpected registered services: %v", services)
	}
	done := make(chan error, 1)
	go func() { done <- host.Serve(listener) }()
	t.Cleanup(func() {
		host.Stop()
		listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("host Serve: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("host did not stop")
		}
	})
	conn, err := grpc.Dial("passthrough:///handler", grpc.WithInsecure(), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return s, pb.NewGNMIClient(conn)
}

func TestGNMIHandlerRPCs(t *testing.T) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	prepareDb(t, ns)
	for _, auth := range []bool{false, true} {
		name := "allowed"
		if auth {
			name = "unauthenticated"
		}
		t.Run(name, func(t *testing.T) {
			s, client := handlerClient(t, &Config{EnableTranslibWrite: true, Threshold: 100, UserAuth: AuthTypes{"jwt": auth}})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := &pb.Path{Elem: []*pb.PathElem{{Name: "COUNTERS"}, {Name: "Ethernet68"}}}
			prefix := &pb.Path{Target: "COUNTERS_DB"}
			want := codes.OK
			if auth {
				want = codes.Unauthenticated
			}
			response, err := client.Get(ctx, &pb.GetRequest{Prefix: prefix, Path: []*pb.Path{path}, Encoding: pb.Encoding_JSON_IETF})
			if status.Code(err) != want {
				t.Fatalf("Get = %v, want %v", err, want)
			}
			if !auth && (len(response.GetNotification()) == 0 || len(response.GetNotification()[0].GetUpdate()) == 0) {
				t.Fatal("Get returned no data")
			}
			// An empty Set still traverses ReqFromMaster and the normal success
			// callback without changing device configuration.
			saved := make(chan struct{}, 1)
			s.SaveStartupConfig = func() error { saved <- struct{}{}; return nil }
			if _, err := client.Set(ctx, &pb.SetRequest{}); status.Code(err) != want {
				t.Fatalf("Set = %v, want %v", err, want)
			}
			if !auth {
				select {
				case <-saved:
				default:
					t.Fatal("successful Set did not save startup configuration")
				}
			} else if len(saved) != 0 {
				t.Fatal("unauthenticated Set saved startup configuration")
			}
			subCtx, subCancel := context.WithCancel(ctx)
			defer subCancel()
			stream, err := client.Subscribe(subCtx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&pb.SubscribeRequest{Request: &pb.SubscribeRequest_Subscribe{Subscribe: &pb.SubscriptionList{
				Prefix: prefix, Subscription: []*pb.Subscription{{Path: path}}, Mode: pb.SubscriptionList_ONCE,
			}}}); err != nil {
				t.Fatal(err)
			}
			updates, synced := 0, false
			if auth {
				response, err := stream.Recv()
				if status.Code(err) != codes.Unauthenticated || response != nil {
					t.Fatalf("unauthenticated Subscribe = (%v, %v), want no response and Unauthenticated", response, err)
				}
			} else {
				for {
					response, err := stream.Recv()
					if err != nil {
						t.Fatalf("Subscribe = %v, want data and sync", err)
					}
					updates += len(response.GetUpdate().GetUpdate())
					synced = synced || response.GetSyncResponse()
					if synced {
						break
					}
				}
			}
			if !auth && (updates == 0 || !synced) {
				t.Fatalf("Subscribe updates=%d, synced=%t", updates, synced)
			}
			subCancel()
			deadline := time.Now().Add(time.Second)
			for {
				s.cMu.Lock()
				remaining := len(s.clients)
				s.cMu.Unlock()
				if remaining == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("Subscribe leaked %d clients", remaining)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}
