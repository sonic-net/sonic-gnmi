package gnmi

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"time"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	ddFileSegSize            int           = 4096
	dlddArtifactWaitTimeout  time.Duration = 5 * time.Minute
	dlddArtifactPollInterval time.Duration = 100 * time.Millisecond
)

func (srv *HealthzServer) getArtifactResolver() artifactPathResolver {
	if srv.artifactResolver.hostMount == "" {
		return defaultArtifactResolver
	}
	return srv.artifactResolver
}

func buildHealthzArtifactHeader(artifactID string, artifact io.ReadSeeker) (*healthz.ArtifactHeader, error) {
	hasher := sha256.New()
	size, err := io.Copy(hasher, artifact)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to hash artifact: %v", err)
	}
	if _, err := artifact.Seek(0, io.SeekStart); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reset artifact file pointer: %v", err)
	}
	return &healthz.ArtifactHeader{
		Id: artifactID,
		ArtifactType: &healthz.ArtifactHeader_File{
			File: &healthz.FileArtifactType{
				Name: artifactID,
				Size: size,
				Hash: &types.HashType{
					Method: types.HashType_SHA256,
					Hash:   hasher.Sum(nil),
				},
			},
		},
	}, nil
}

func waitForDLDDArtifact(
	ctx context.Context,
	resolver artifactPathResolver,
	artifactID string,
	timeout time.Duration,
	interval time.Duration,
) (*os.File, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		file, _, err := resolver.open(artifactID)
		if err == nil {
			return file, nil
		}
		if filepath.IsAbs(artifactID) || status.Code(err) != codes.NotFound {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-deadline.C:
			return nil, status.Error(codes.NotFound, "artifact was not ready before the wait deadline")
		case <-time.After(interval):
		}
	}
}

func (srv *HealthzServer) Artifact(req *healthz.ArtifactRequest, stream healthz.Healthz_ArtifactServer) error {
	if _, err := authenticate(srv.config, stream.Context(), "gnoi", false); err != nil {
		log.Errorf("Healthz.Artifact authentication failed: %v", err)
		return err
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "Healthz.Artifact received a nil request")
	}

	artifactID := req.GetId()
	log.V(1).Infof("Artifact RPC Get request ID: %+v", artifactID)
	f, err := waitForDLDDArtifact(
		stream.Context(),
		srv.getArtifactResolver(),
		artifactID,
		dlddArtifactWaitTimeout,
		dlddArtifactPollInterval,
	)
	if err != nil {
		return err
	}
	defer f.Close()
	artifactHeader, err := buildHealthzArtifactHeader(artifactID, f)
	if err != nil {
		return err
	}

	header := &healthz.ArtifactResponse{
		Contents: &healthz.ArtifactResponse_Header{
			Header: artifactHeader,
		},
	}
	if err := stream.Send(header); err != nil {
		log.Errorf("failed to send artifact header: %v", err)
		return err
	}

	buf := make([]byte, ddFileSegSize)
	sentContent := false
	for {
		if err := stream.Context().Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		n, err := f.Read(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Errorf("failed to read artifact: %v", err)
			return status.Errorf(codes.Internal, "artifact read error: %v", err)
		}
		content := &healthz.ArtifactResponse{
			Contents: &healthz.ArtifactResponse_Bytes{
				Bytes: buf[:n],
			},
		}
		if err := stream.Send(content); err != nil {
			log.Errorf("failed to send artifact data: %v", err)
			return err
		}
		sentContent = true
	}
	// Healthz requires one or more bytes/proto messages between the header and
	// trailer. Preserve that protocol ordering even for a valid empty file.
	if !sentContent {
		if err := stream.Send(&healthz.ArtifactResponse{
			Contents: &healthz.ArtifactResponse_Bytes{Bytes: []byte{}},
		}); err != nil {
			log.Errorf("failed to send empty artifact data: %v", err)
			return err
		}
	}

	trailer := &healthz.ArtifactResponse{
		Contents: &healthz.ArtifactResponse_Trailer{
			Trailer: &healthz.ArtifactTrailer{},
		},
	}
	if err := stream.Send(trailer); err != nil {
		log.Errorf("failed to send artifact trailer: %v", err)
		return err
	}
	log.Infof("Successfully streamed artifact ID %q (size=%d bytes)", artifactID, artifactHeader.GetFile().GetSize())
	return nil
}
