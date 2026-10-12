package gnmi

import (
	"context"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
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
	ddFileSegSize               int           = 4096
	healthzArtifactWaitTimeout  time.Duration = 5 * time.Minute
	healthzArtifactPollInterval time.Duration = 100 * time.Millisecond
)

func (srv *HealthzServer) getArtifactResolver() artifactPathResolver {
	if srv.artifactResolver.hostMount == "" {
		return defaultArtifactResolver
	}
	return srv.artifactResolver
}

func buildHealthzArtifactHeader(ctx context.Context, artifactID string, artifact io.ReadSeeker) (*healthz.ArtifactHeader, error) {
	// Fix the transfer size before hashing, including for larger legacy archives.
	size, err := artifact.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to determine artifact size: %v", err)
	}
	if _, err := artifact.Seek(0, io.SeekStart); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reset artifact file pointer: %v", err)
	}
	hasher := sha256.New()
	buf := make([]byte, 32*1024)
	for remaining := size; remaining > 0; {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		n, err := io.ReadFull(artifact, buf[:min(int64(len(buf)), remaining)])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to hash artifact: %v", err)
		}
		hasher.Write(buf[:n])
		remaining -= int64(n)
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if _, err := artifact.Seek(0, io.SeekStart); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to reset artifact file pointer: %v", err)
	}
	mimeType := "application/octet-stream"
	if strings.HasSuffix(artifactID, ".tar.gz") {
		mimeType = "application/gzip"
	}
	return &healthz.ArtifactHeader{
		Id: artifactID,
		ArtifactType: &healthz.ArtifactHeader_File{
			File: &healthz.FileArtifactType{
				Name:     filepath.Base(artifactID),
				Mimetype: mimeType,
				Size:     size,
				Hash: &types.HashType{
					Method: types.HashType_SHA256,
					Hash:   hasher.Sum(nil),
				},
			},
		},
	}, nil
}

func waitForHealthzArtifact(
	ctx context.Context,
	resolver artifactPathResolver,
	artifactID string,
	timeout time.Duration,
	interval time.Duration,
	checkState func(string) (string, error),
) (*os.File, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	returnOpen := func(file *os.File) (*os.File, error) {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, status.FromContextError(err).Err()
		}
		return file, nil
	}
	var lastStatusCheck time.Time
	for {
		if err := ctx.Err(); err != nil {
			return nil, status.FromContextError(err).Err()
		}
		file, _, err := resolver.open(artifactID)
		if err == nil {
			return returnOpen(file)
		}
		if filepath.IsAbs(artifactID) || status.Code(err) != codes.NotFound {
			return nil, err
		}
		if checkState != nil && opaqueArtifactIDPattern.MatchString(artifactID) &&
			time.Since(lastStatusCheck) >= time.Second {
			type stateResult struct {
				state string
				err   error
			}
			result := make(chan stateResult, 1)
			// The D-Bus call has its own timeout; let a canceled RPC return first.
			go func() {
				state, err := checkState(artifactID)
				result <- stateResult{state, err}
			}()
			var state string
			select {
			case <-ctx.Done():
				return nil, status.FromContextError(ctx.Err()).Err()
			case <-deadline.C:
				return nil, status.Error(codes.NotFound, "artifact was not ready before the wait deadline")
			case outcome := <-result:
				state, err = outcome.state, outcome.err
			}
			if err := ctx.Err(); err != nil {
				return nil, status.FromContextError(err).Err()
			}
			if err != nil {
				if status.Code(err) == codes.DeadlineExceeded {
					// Archive submission can outlast one host status call.
					lastStatusCheck = time.Now()
					continue
				}
				return nil, err
			}
			switch state {
			case "MISSING":
				return nil, status.Error(codes.NotFound, "artifact not found")
			case "PENDING":
			case "COMPLETED":
				// Publication can finish during the status call. Reopen once before
				// reporting a completed archive that the container cannot access.
				file, _, err := resolver.open(artifactID)
				if err == nil {
					return returnOpen(file)
				}
				if status.Code(err) != codes.NotFound {
					return nil, err
				}
				return nil, status.Error(codes.Internal, "completed Healthz artifact is unavailable in gNMI")
			default:
				return nil, status.Errorf(codes.Internal, "invalid Healthz artifact state %q", state)
			}
			lastStatusCheck = time.Now()
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

func healthzArtifactState(artifactID string) (string, error) {
	var result struct {
		State string `json:"state"`
	}
	if err := callHealthzCatalog(ssc.Service.HealthzArtifactStatus,
		map[string]string{"artifact_id": artifactID}, &result); err != nil {
		return "", err
	}
	return result.State, nil
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
	f, err := waitForHealthzArtifact(
		stream.Context(),
		srv.getArtifactResolver(),
		artifactID,
		healthzArtifactWaitTimeout,
		healthzArtifactPollInterval,
		healthzArtifactState,
	)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := stream.Context().Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	artifactHeader, err := buildHealthzArtifactHeader(stream.Context(), artifactID, f)
	if err != nil {
		return err
	}
	if err := stream.Context().Err(); err != nil {
		return status.FromContextError(err).Err()
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
	for remaining := artifactHeader.GetFile().GetSize(); remaining > 0; {
		if err := stream.Context().Err(); err != nil {
			return status.FromContextError(err).Err()
		}
		n, err := io.ReadFull(f, buf[:min(int64(len(buf)), remaining)])
		if err != nil {
			log.Errorf("failed to read artifact: %v", err)
			return status.Errorf(codes.Internal, "artifact read error: %v", err)
		}
		if err := stream.Context().Err(); err != nil {
			return status.FromContextError(err).Err()
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
		remaining -= int64(n)
		sentContent = true
	}
	if err := stream.Context().Err(); err != nil {
		return status.FromContextError(err).Err()
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
	if err := stream.Context().Err(); err != nil {
		return status.FromContextError(err).Err()
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
