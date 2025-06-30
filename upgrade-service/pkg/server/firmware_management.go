package server

import (
	"context"
	"fmt"
	"os"
	"regexp"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/config"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

type firmwareManagementServer struct {
	pb.UnimplementedFirmwareManagementServer
}

func NewFirmwareManagementServer() pb.FirmwareManagementServer {
	return &firmwareManagementServer{}
}

func (s *firmwareManagementServer) CleanupOldFirmware(
	ctx context.Context,
	req *pb.CleanupOldFirmwareRequest,
) (*pb.CleanupOldFirmwareResponse, error) {
	result := firmware.CleanupOldFirmware()

	return &pb.CleanupOldFirmwareResponse{
		FilesDeleted:    result.FilesDeleted,
		DeletedFiles:    result.DeletedFiles,
		Errors:          result.Errors,
		SpaceFreedBytes: result.SpaceFreedBytes,
	}, nil
}

func (s *firmwareManagementServer) ListFirmwareImages(
	ctx context.Context,
	req *pb.ListFirmwareImagesRequest,
) (*pb.ListFirmwareImagesResponse, error) {
	glog.V(1).Infof("ListFirmwareImages request: directories=%v, pattern=%s",
		req.SearchDirectories, req.VersionPattern)

	// Get search results from firmware package
	var searchResults []*firmware.ImageSearchResult
	var searchErrors []string
	var err error

	// Handle custom directories if specified
	if len(req.SearchDirectories) > 0 {
		searchResults, searchErrors = searchCustomDirectories(req.SearchDirectories)
	} else {
		// Use default search
		searchResults, err = firmware.FindAllImages()
		if err != nil {
			glog.Errorf("Failed to search for firmware images: %v", err)
			return nil, err
		}
	}

	// Apply version pattern filter if specified
	if req.VersionPattern != "" {
		searchResults, err = filterByVersionPattern(searchResults, req.VersionPattern)
		if err != nil {
			glog.Errorf("Failed to apply version pattern filter: %v", err)
			return nil, err
		}
	}

	// Convert results to protobuf format
	pbImages := make([]*pb.FirmwareImageInfo, 0, len(searchResults))
	for _, result := range searchResults {
		pbImages = append(pbImages, &pb.FirmwareImageInfo{
			FilePath:      result.FilePath,
			Version:       result.VersionInfo.Version,
			FullVersion:   result.VersionInfo.FullVersion,
			ImageType:     result.VersionInfo.ImageType,
			FileSizeBytes: result.FileSize,
		})
	}

	response := &pb.ListFirmwareImagesResponse{
		Images: pbImages,
		Errors: searchErrors,
	}

	glog.V(1).Infof("ListFirmwareImages response: found %d images", len(pbImages))
	return response, nil
}

// searchCustomDirectories searches for firmware images in custom directories.
func searchCustomDirectories(directories []string) ([]*firmware.ImageSearchResult, []string) {
	var errors []string

	// Check directory existence first
	for _, dir := range directories {
		dirPath := paths.ToHost(dir, config.Global.RootFS)
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			errors = append(errors, fmt.Sprintf("directory does not exist: %s", dir))
		}
	}

	// Save original configuration
	originalDirs := firmware.DefaultSearchDirectories
	defer func() { firmware.DefaultSearchDirectories = originalDirs }()

	// Use custom directories
	firmware.DefaultSearchDirectories = directories

	// Perform search
	results, err := firmware.FindAllImages()
	if err != nil {
		errors = append(errors, err.Error())
		// Continue with empty results rather than fail
		results = []*firmware.ImageSearchResult{}
	}

	return results, errors
}

// filterByVersionPattern filters search results by regex pattern.
func filterByVersionPattern(
	results []*firmware.ImageSearchResult,
	pattern string,
) ([]*firmware.ImageSearchResult, error) {
	// Compile regex pattern
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}

	// Filter results
	filtered := make([]*firmware.ImageSearchResult, 0)
	for _, result := range results {
		// Check both version and full_version for matches
		if regex.MatchString(result.VersionInfo.Version) || regex.MatchString(result.VersionInfo.FullVersion) {
			filtered = append(filtered, result)
		}
	}

	return filtered, nil
}

func (s *firmwareManagementServer) ConsolidateImages(
	ctx context.Context,
	req *pb.ConsolidateImagesRequest,
) (*pb.ConsolidateImagesResponse, error) {
	glog.V(1).Infof("ConsolidateImages request: dry_run=%t", req.DryRun)

	// Create consolidation service with default configuration
	consolidationService := firmware.NewConsolidationService()

	// Perform consolidation
	result, err := consolidationService.ConsolidateImages(req.DryRun)
	if err != nil {
		glog.Errorf("Failed to consolidate images: %v", err)
		return nil, err
	}

	// Convert result to protobuf response
	response := &pb.ConsolidateImagesResponse{
		CurrentImage:    result.CurrentImage,
		RemovedImages:   result.RemovedImages,
		SpaceFreedBytes: result.SpaceFreedBytes,
		Warnings:        result.Warnings,
		Executed:        result.Executed,
	}

	glog.V(1).Infof("ConsolidateImages response: current=%s, removed=%d, executed=%t, space_freed=%d",
		response.CurrentImage, len(response.RemovedImages), response.Executed, response.SpaceFreedBytes)

	return response, nil
}

func (s *firmwareManagementServer) ListImages(
	ctx context.Context,
	req *pb.ListImagesRequest,
) (*pb.ListImagesResponse, error) {
	glog.V(1).Info("ListImages request")

	// Create sonic-installer wrapper
	sonicInstaller := installer.NewSonicInstaller()

	// Get installed images using sonic-installer list
	listResult, err := sonicInstaller.List()
	if err != nil {
		glog.Errorf("Failed to list images: %v", err)
		return nil, err
	}

	// Extract image names
	imageNames := make([]string, 0, len(listResult.Images))
	for _, img := range listResult.Images {
		imageNames = append(imageNames, img.Name)
	}

	response := &pb.ListImagesResponse{
		Images:       imageNames,
		CurrentImage: listResult.Current,
		NextImage:    listResult.Next,
		Warnings:     []string{}, // No warnings for now
	}

	glog.V(1).Infof("ListImages response: found %d images, current=%s, next=%s",
		len(imageNames), response.CurrentImage, response.NextImage)

	return response, nil
}
