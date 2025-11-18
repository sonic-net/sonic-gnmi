package dpuproxy

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const (
	// MetadataKeyTargetType is the metadata key for specifying target type (e.g., "dpu", "npu")
	MetadataKeyTargetType = "x-sonic-ss-target-type"

	// MetadataKeyTargetIndex is the metadata key for specifying target index (e.g., "0", "1")
	MetadataKeyTargetIndex = "x-sonic-ss-target-index"

	// TargetTypeDPU indicates the request should be routed to a DPU
	TargetTypeDPU = "dpu"
)

// TargetMetadata contains the extracted routing metadata from a gRPC request
type TargetMetadata struct {
	// TargetType specifies where to route the request (e.g., "dpu")
	TargetType string

	// TargetIndex specifies which target instance (e.g., "0" for DPU0)
	TargetIndex string

	// HasMetadata indicates if any routing metadata was found
	HasMetadata bool
}

// ExtractTargetMetadata extracts routing metadata from the gRPC context.
// It looks for x-sonic-ss-target-type and x-sonic-ss-target-index headers.
// Returns TargetMetadata with HasMetadata=true if any routing headers are present.
func ExtractTargetMetadata(ctx context.Context) TargetMetadata {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return TargetMetadata{HasMetadata: false}
	}

	targetType := ""
	targetIndex := ""

	// Extract target type
	if values := md.Get(MetadataKeyTargetType); len(values) > 0 {
		targetType = values[0]
	}

	// Extract target index
	if values := md.Get(MetadataKeyTargetIndex); len(values) > 0 {
		targetIndex = values[0]
	}

	// Consider metadata present if either field is set
	hasMetadata := targetType != "" || targetIndex != ""

	return TargetMetadata{
		TargetType:  targetType,
		TargetIndex: targetIndex,
		HasMetadata: hasMetadata,
	}
}

// IsDPUTarget returns true if the metadata indicates routing to a DPU
func (tm TargetMetadata) IsDPUTarget() bool {
	return tm.HasMetadata && tm.TargetType == TargetTypeDPU
}
