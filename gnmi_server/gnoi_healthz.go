package gnmi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	//"os/exec"
	//"path/filepath"
	"strings"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	compKey           string        = "name"
	ddComponentKey    string        = "component"
	ddComponentAll    string        = "all"
	ddLogLvlKey       string        = "level"
	ddLogLvlAlert     string        = "alert"
	ddLogLvlCritical  string        = "critical"
	ddLogLvlAll       string        = "all"
	ddLogLvlSuf       string        = "-info"
	ddFileSegSize     int           = 4096
	artifactSleepTime time.Duration = 5 * time.Second
)

var (
	artifactColTimeout time.Duration = 5 * time.Minute
)

func isDebugData(p *types.Path) bool {
	log.V(1).Info("Inside gNOI: healthz isDebugData")
	fmt.Printf("Checking isDebugData, path: %+v\n", p)

	if p == nil {
		return false
	}

	elems := p.GetElem()
	for i, e := range elems {
		fmt.Printf("elem[%d]: name=%s, keys=%v\n", i, e.GetName(), e.GetKey())
	}

	if len(elems) != 4 {
		return false
	}
	if elems[0].GetName() != "components" || len(elems[0].GetKey()) > 0 {
		return false
	}
	if elems[1].GetName() != "component" || len(elems[1].GetKey()) != 1 {
		return false
	}
	if _, ok := elems[1].GetKey()["name"]; !ok {
		return false
	}
	if elems[2].GetName() != "healthz" || len(elems[2].GetKey()) > 0 {
		return false
	}
	if (elems[3].GetName() != ddLogLvlAlert+ddLogLvlSuf && elems[3].GetName() != ddLogLvlCritical+ddLogLvlSuf && elems[3].GetName() != ddLogLvlAll+ddLogLvlSuf) || len(elems[3].GetKey()) > 0 {
		return false
	}
	return true
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return io.ReadAll(f)
}

func waitForArtifact(file string) error {
	log.V(1).Info("Inside waitforartifact:%s\n", file)
	sc, err := ssc.NewDbusClient()
	if err != nil {
		return err
	}
	for start := time.Now(); time.Since(start) < artifactColTimeout; {
		if _, err := sc.HealthzCheck(file); err == nil {
			return nil
		}
		time.Sleep(artifactSleepTime)
	}
	return fmt.Errorf("Artifact collection timeout on file %v", file)
}

func getDebugData(p *types.Path) (*healthz.GetResponse, error) {
	log.V(1).Info("Inside gNOI: healthz getDebugData")
	c := ddComponentAll
	ll := ddLogLvlAlert
	elems := p.GetElem()
	if len(elems) == 4 {
		c, _ = elems[1].GetKey()["name"]
		ll = strings.TrimSuffix(elems[3].GetName(), ddLogLvlSuf)
	}
	req := map[string]string{
		ddComponentKey:           c,
		ddLogLvlKey:              ll,
		"use_persistent_storage": "true",
	}
	b, err := json.Marshal(req)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Error: %v", err)
	}

	log.V(1).Info("gNOI: healthz calling BE service")
	sc, err := ssc.NewDbusClient()
	if err != nil {
		log.Errorf("getDebugData(): failed to create D-Bus client: %v", err)
		return nil, err
	}
	log.V(1).Info("getDebugData(): D-Bus client created")

	fmt.Printf("DEBUG JSON sent to D-Bus: %q\n", string(b))
	s, err := sc.HealthzCollect(string(b))
	//s, err := sc.HealthzCollect(fmt.Sprintf("%s", string(b)))
	//s, err := sc.HealthzCollect(fmt.Sprintf("[\"%s\"]", string(b)))
	if err != nil {
		log.Errorf("getDebugData(): sc.HealthzCollect() failed: %v", err)
		return nil, status.Errorf(codes.Internal, "Host service error: %v", err)
	}
	log.V(1).Infof("getDebugData(): artifact path returned: %s", s)

	// Wait for artifact file to be ready.
	log.V(1).Info("getDebugData(): waiting for artifact to be ready")

	if err := waitForArtifact(s); err != nil {
		log.Errorf("getDebugData(): waitForArtifact failed: %v", err)
		return nil, status.Errorf(codes.Internal, "Error: %v", err)
	}
	log.V(1).Info("getDebugData(): artifact ready")
	log.Infof("Trying to read artifact file: %s", s)

	fi, err := readFile(s)
	if err != nil {
		fmt.Sprintf("Failed to read file: %v", err)
	} else {
		fmt.Sprintf("File read success, size: %d bytes", len(fi))
	}
	/*
		fi, err := os.ReadFile(s)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Error: [%w]", err)
		}*/

	h := sha256.Sum256(fi)

	log.V(1).Info("gNOI:Before healthz get response")
	resp := &healthz.GetResponse{}
	resp.Component = &healthz.ComponentStatus{
		Path:   p,
		Id:     s,
		Status: healthz.Status_STATUS_HEALTHY,
		Artifacts: []*healthz.ArtifactHeader{
			&healthz.ArtifactHeader{
				Id: s,
				ArtifactType: &healthz.ArtifactHeader_File{
					File: &healthz.FileArtifactType{
						Name: s,
						Size: int64(len(fi)),
						Hash: &types.HashType{
							Method: types.HashType_SHA256,
							Hash:   h[:],
						},
					},
				},
			},
		},
	}
	log.V(1).Info("getDebugData(): response successfully constructed")
	return resp, nil
}

// Get implements the corresponding RPC.
func (srv *HealthzServer) Get(ctx context.Context, req *healthz.GetRequest) (*healthz.GetResponse, error) {
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("gNOI: healthz.Get")

	if req == nil || req.GetPath() == nil {
		return nil, status.Errorf(codes.InvalidArgument, "Healthz.Get received nil request or path")
	}
	path := req.GetPath()

	if isDebugData(path) {
		return getDebugData(path)
	}
	return nil, status.Errorf(codes.Unimplemented, "Healthz.Get is unimplemented for component: [%s].", path.GetElem())
}

/*
	switch {
	case isDebugData(req.GetPath()):
		return getDebugData(req.GetPath())
	default:
		return nil, status.Errorf(codes.Unimplemented, fmt.Sprintf("Healthz.Get is unimplemented for component: [%s].", req.GetPath()))
	}*/

// Acknowledge implements the corresponding RPC.
func (srv *HealthzServer) Acknowledge(ctx context.Context, req *healthz.AcknowledgeRequest) (*healthz.AcknowledgeResponse, error) {
	log.V(1).Infof("Inside Healthz Acknowledge RPC\n")
	fmt.Printf("Printing Ack request: %+v\n", req)
	ctx, err := authenticate(srv.config, ctx, "gnoi", false)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("gNOI: healthz.Acknowledge")

	sc, err := ssc.NewDbusClient()
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Calling HealthzACK FE\n")
	fmt.Printf("Printing Ack ID sent to DBUS HealthzAck:%s\n", req.GetId())

	_, err = sc.HealthzAck(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Host service error: %v", err)
	}

	return &healthz.AcknowledgeResponse{}, nil
}

// Artifact implements the corresponding RPC.
func (srv *HealthzServer) Artifact(req *healthz.ArtifactRequest, stream healthz.Healthz_ArtifactServer) error {
	log.V(1).Infof("Inside Healthz Artifact FE\n")
	log.V(1).Infof("Printing Artifact Request:%s %s\n", req, req.GetId())
	file := req.GetId()
	fi, err := readFile(file)
	if err != nil {
		fmt.Sprintf("Failed to read file:%v\n", err)
	} else {
		fmt.Sprintf("File read success size:%d\n", len(fi))
	}

	/*
		fi, err := os.ReadFile(file)
		if err != nil {
			return status.Errorf(codes.NotFound, "File %v not found: [%v].", file, err.Error())
		}*/
	h := sha256.Sum256(fi)
	log.V(1).Infof("Collecting Artifact header\n")
	header := &healthz.ArtifactResponse{
		Contents: &healthz.ArtifactResponse_Header{
			Header: &healthz.ArtifactHeader{
				Id: file,
				ArtifactType: &healthz.ArtifactHeader_File{
					File: &healthz.FileArtifactType{
						Name: file,
						Size: int64(len(fi)),
						Hash: &types.HashType{
							Method: types.HashType_SHA256,
							Hash:   h[:],
						},
					},
				},
			},
		},
	}
	if err := stream.Send(header); err != nil {
		return err
	}

	for idx := 0; idx < len(fi); idx += ddFileSegSize {
		end := idx + ddFileSegSize
		if end > len(fi) {
			end = len(fi)
		}
		content := &healthz.ArtifactResponse{
			Contents: &healthz.ArtifactResponse_Bytes{
				Bytes: fi[idx:end],
			},
		}
		if err := stream.Send(content); err != nil {
			return err
		}
	}

	trailer := &healthz.ArtifactResponse{
		Contents: &healthz.ArtifactResponse_Trailer{
			Trailer: &healthz.ArtifactTrailer{},
		},
	}
	if err := stream.Send(trailer); err != nil {
		return err
	}

	return nil
}

func (srv *HealthzServer) List(ctx context.Context, req *healthz.ListRequest) (*healthz.ListResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "gNOI Healthz List not implemented")
}

func (srv *HealthzServer) Check(ctx context.Context, req *healthz.CheckRequest) (*healthz.CheckResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "gNOI Healthz Check not implemented")
}
