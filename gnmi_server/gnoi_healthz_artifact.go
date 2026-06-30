package gnmi

import (
	"crypto/sha256"
	"io"

	log "github.com/golang/glog"
	"github.com/openconfig/gnoi/healthz"
	types "github.com/openconfig/gnoi/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	ddFileSegSize int = 4096
)

var (
	healthzArtifactAuthenticate = authenticate
)

func (srv *HealthzServer) getArtifactResolver() artifactPathResolver {
	if srv.artifactResolver.hostMount == "" {
		return defaultArtifactResolver
	}
	return srv.artifactResolver
}

func (srv *HealthzServer) Artifact(req *healthz.ArtifactRequest, stream healthz.Healthz_ArtifactServer) error {
	if _, err := healthzArtifactAuthenticate(srv.config, stream.Context(), "gnoi", false); err != nil {
		log.Errorf("Healthz.Artifact authentication failed: %v", err)
		return err
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "Healthz.Artifact received a nil request")
	}

	artifactID := req.GetId()
	log.V(1).Infof("Artifact RPC Get request ID: %+v", artifactID)
	f, filePath, err := srv.getArtifactResolver().open(artifactID)
	if err != nil {
		return err
	}
	defer f.Close()
	fileInfo, err := f.Stat()
	if err != nil {
		return status.Errorf(codes.Internal, "failed to stat artifact: %v", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return status.Error(codes.InvalidArgument, "artifact is not a regular file")
	}

	hasher := sha256.New()
	size, err := io.Copy(hasher, f)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to hash artifact: %v", err)
	}
	hashSum := hasher.Sum(nil)

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return status.Errorf(codes.Internal, "failed to reset artifact file pointer: %v", err)
	}

	header := &healthz.ArtifactResponse{
		Contents: &healthz.ArtifactResponse_Header{
			Header: &healthz.ArtifactHeader{
				Id: artifactID,
				ArtifactType: &healthz.ArtifactHeader_File{
					File: &healthz.FileArtifactType{
						Name: artifactID,
						Size: size,
						Hash: &types.HashType{
							Method: types.HashType_SHA256,
							Hash:   hashSum,
						},
					},
				},
			},
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
	log.Infof("Successfully streamed artifact ID %q (size=%d bytes)", artifactID, size)
	return nil
}
