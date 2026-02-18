package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/download"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/firmware"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/installer"
	"github.com/sonic-net/sonic-gnmi/upgrade-service/internal/paths"
	pb "github.com/sonic-net/sonic-gnmi/upgrade-service/proto"
)

// Global download session state - supports multiple parallel downloads.
var (
	downloadSessions = make(map[string]*downloadSessionInfo)
	downloadMutex    sync.RWMutex
)

// downloadSessionInfo tracks a single download session.
type downloadSessionInfo struct {
	Session   *download.DownloadSession
	Result    *download.DownloadResult
	Error     *download.DownloadError
	Done      bool
	StartTime time.Time
}

type firmwareManagementServer struct {
	pb.UnimplementedFirmwareManagementServer
	rootFS string
}

func NewFirmwareManagementServer(rootFS string) pb.FirmwareManagementServer {
	return &firmwareManagementServer{rootFS: rootFS}
}

func (s *firmwareManagementServer) CleanupOldFirmware(
	ctx context.Context,
	req *pb.CleanupOldFirmwareRequest,
) (*pb.CleanupOldFirmwareResponse, error) {
	// Resolve directory paths
	dirPaths := []string{
		paths.ToHost("/host", s.rootFS),
		paths.ToHost("/tmp", s.rootFS),
	}
	extensions := []string{"*.bin", "*.swi", "*.rpm"}

	result := firmware.CleanupOldFirmwareInDirectories(dirPaths, extensions)

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
		searchResults, searchErrors = s.searchCustomDirectories(req.SearchDirectories)
	} else {
		// Use default search with resolved paths
		dirPaths := []string{
			paths.ToHost("/host", s.rootFS),
			paths.ToHost("/tmp", s.rootFS),
		}
		extensions := []string{"*.bin", "*.swi"}

		searchResults, err = firmware.FindAllImagesInDirectories(dirPaths, extensions)
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

// extractFilenameFromURL extracts the filename from a URL for firmware downloads.
func extractFilenameFromURL(urlStr string) (string, error) {
	// Parse the URL to extract filename
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	// Extract filename from path
	filename := filepath.Base(parsedURL.Path)
	if filename == "" || filename == "." || filename == "/" {
		return "", fmt.Errorf("cannot determine filename from URL")
	}

	return filename, nil
}

func (s *firmwareManagementServer) DownloadFirmware(
	ctx context.Context,
	req *pb.DownloadFirmwareRequest,
) (*pb.DownloadFirmwareResponse, error) {
	glog.V(1).Infof("DownloadFirmware request: url=%s, output_path=%s", req.Url, req.OutputPath)

	// Validate URL
	if req.Url == "" {
		return nil, fmt.Errorf("url is required")
	}

	// Create the session immediately for tracking
	session := &download.DownloadSession{
		ID:         fmt.Sprintf("download-%d", time.Now().UnixNano()),
		URL:        req.Url,
		OutputPath: "", // Will be set after path resolution
		Status:     "starting",
		StartTime:  time.Now(),
		LastUpdate: time.Now(),
	}

	// Create download configuration
	config := download.DefaultDownloadConfig()
	// Keep default interface binding like cmd/test/download
	if req.ConnectTimeoutSeconds > 0 {
		config.ConnectTimeout = time.Duration(req.ConnectTimeoutSeconds) * time.Second
	}
	if req.TotalTimeoutSeconds > 0 {
		config.OverallTimeout = time.Duration(req.TotalTimeoutSeconds) * time.Second
	}
	// Set expected MD5 checksum if provided
	config.ExpectedMD5 = req.ExpectedMd5

	// Resolve output path
	outputPath := req.OutputPath
	if outputPath == "" {
		// Auto-detect filename from URL and save to /host
		filename, err := extractFilenameFromURL(req.Url)
		if err != nil {
			return nil, fmt.Errorf("failed to determine filename from URL: %v", err)
		}
		outputPath = paths.ToHost(filepath.Join("/host", filename), s.rootFS)
	} else {
		// Resolve user-provided path
		outputPath = paths.ToHost(outputPath, s.rootFS)
	}

	// Update session with resolved output path
	session.OutputPath = outputPath

	// Create a clean background context with timeout - similar to cmd/test/download
	downloadCtx := context.Background()
	if req.TotalTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		downloadCtx, cancel = context.WithTimeout(context.Background(), time.Duration(req.TotalTimeoutSeconds)*time.Second)
		// Let the context timeout naturally - don't defer cancel since the goroutine manages its own lifecycle
		_ = cancel // Mark as used to avoid vet warning
	}

	// Initialize session info and store in sessions map
	sessionInfo := &downloadSessionInfo{
		Session:   session,
		StartTime: time.Now(),
		Done:      false,
	}

	// Store session in the sessions map
	downloadMutex.Lock()
	downloadSessions[session.ID] = sessionInfo
	downloadMutex.Unlock()

	// Start download in goroutine
	go func(sessionID string) {
		downloadSession, result, err := download.DownloadFirmwareWithConfig(downloadCtx, req.Url, outputPath, config)

		// Update session info with results
		downloadMutex.Lock()
		sessionInfo, exists := downloadSessions[sessionID]
		if !exists {
			downloadMutex.Unlock()
			glog.Errorf("Session %s not found during download completion", sessionID)
			return
		}

		// Copy progress from the actual download session
		if downloadSession != nil {
			sessionInfo.Session.Downloaded = downloadSession.Downloaded
			sessionInfo.Session.Total = downloadSession.Total
			sessionInfo.Session.SpeedBytesPerSec = downloadSession.SpeedBytesPerSec
			sessionInfo.Session.Status = downloadSession.Status
			sessionInfo.Session.CurrentMethod = downloadSession.CurrentMethod
			sessionInfo.Session.AttemptNumber = downloadSession.AttemptNumber
			sessionInfo.Session.LastUpdate = downloadSession.LastUpdate
		}
		sessionInfo.Result = result
		if err != nil {
			if downloadErr, ok := err.(*download.DownloadError); ok {
				sessionInfo.Error = downloadErr
			} else {
				// Convert generic error to download error
				sessionInfo.Error = download.NewOtherError(req.Url, err.Error(), nil)
			}
		}
		sessionInfo.Done = true
		downloadMutex.Unlock()

		if err != nil {
			glog.Errorf("Download failed for %s: %v", req.Url, err)
		} else {
			glog.V(1).Infof("Download completed successfully for %s: %s", req.Url, result.FilePath)
		}
	}(session.ID)

	sessionID := session.ID
	status := "starting"

	return &pb.DownloadFirmwareResponse{
		SessionId:  sessionID,
		Status:     status,
		OutputPath: outputPath,
	}, nil
}

func (s *firmwareManagementServer) GetDownloadStatus(
	ctx context.Context,
	req *pb.GetDownloadStatusRequest,
) (*pb.GetDownloadStatusResponse, error) {
	glog.V(1).Infof("GetDownloadStatus request: session_id=%s", req.SessionId)

	// Validate session ID
	if req.SessionId == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	downloadMutex.RLock()
	defer downloadMutex.RUnlock()

	// Find the session by ID
	sessionInfo, exists := downloadSessions[req.SessionId]
	if !exists {
		return nil, fmt.Errorf("download session not found: %s", req.SessionId)
	}

	// Check if session is initialized
	if sessionInfo.Session == nil {
		return nil, fmt.Errorf("download session not initialized: %s", req.SessionId)
	}

	response := &pb.GetDownloadStatusResponse{
		SessionId: req.SessionId,
	}

	// Convert session state to protobuf oneof
	if !sessionInfo.Done {
		// Download in progress or starting
		downloaded, total, speed, status := sessionInfo.Session.GetProgress()
		if status == "starting" {
			response.State = &pb.GetDownloadStatusResponse_Starting{
				Starting: &pb.DownloadStarting{
					Message:   "Download is starting",
					StartTime: sessionInfo.StartTime.Format(time.RFC3339),
				},
			}
		} else {
			response.State = &pb.GetDownloadStatusResponse_Progress{
				Progress: convertProgressToProto(sessionInfo.Session, downloaded, total, speed),
			}
		}
		return response, nil
	}

	// Download completed - handle success/failure
	if sessionInfo.Error != nil {
		// Download failed
		response.State = &pb.GetDownloadStatusResponse_Error{
			Error: convertErrorToProto(sessionInfo.Error),
		}
	} else if sessionInfo.Result != nil {
		// Download completed successfully
		response.State = &pb.GetDownloadStatusResponse_Result{
			Result: convertResultToProto(sessionInfo.Result),
		}
	} else {
		// Shouldn't happen, but handle gracefully
		response.State = &pb.GetDownloadStatusResponse_Error{
			Error: &pb.DownloadError{
				Category: "other",
				Message:  "download completed but no result available",
				Url:      sessionInfo.Session.URL,
			},
		}
	}

	return response, nil
}

// convertProgressToProto converts download progress to protobuf message.
func convertProgressToProto(
	session *download.DownloadSession, downloaded, total int64, speed float64,
) *pb.DownloadProgress {
	progress := &pb.DownloadProgress{
		DownloadedBytes:  downloaded,
		TotalBytes:       total,
		SpeedBytesPerSec: speed,
		StartTime:        session.StartTime.Format(time.RFC3339),
		LastUpdate:       session.LastUpdate.Format(time.RFC3339),
	}

	// Calculate percentage if total is known
	if total > 0 {
		progress.Percentage = float64(downloaded) / float64(total) * 100.0
	}

	// Get current method and attempt count safely
	progress.CurrentMethod = session.CurrentMethod
	progress.AttemptCount = int32(session.AttemptNumber)

	return progress
}

// convertResultToProto converts download result to protobuf message.
func convertResultToProto(result *download.DownloadResult) *pb.DownloadResult {
	return &pb.DownloadResult{
		FilePath:      result.FilePath,
		FileSizeBytes: result.FileSize,
		DurationMs:    result.Duration.Milliseconds(),
		AttemptCount:  int32(result.AttemptCount),
		FinalMethod:   result.FinalMethod,
		Url:           result.URL,
		ChecksumValidation: &pb.ChecksumValidation{
			ValidationRequested: result.ChecksumValidation.ValidationRequested,
			ValidationPassed:    result.ChecksumValidation.ValidationPassed,
			ExpectedChecksum:    result.ChecksumValidation.ExpectedChecksum,
			ActualChecksum:      result.ChecksumValidation.ActualChecksum,
			Algorithm:           result.ChecksumValidation.Algorithm,
		},
	}
}

// convertErrorToProto converts download error to protobuf message.
func convertErrorToProto(err *download.DownloadError) *pb.DownloadError {
	pbError := &pb.DownloadError{
		Category: string(err.Category),
		HttpCode: int32(err.Code),
		Message:  err.Message,
		Url:      err.URL,
		Attempts: make([]*pb.DownloadAttempt, len(err.Attempts)),
	}

	// Convert attempts
	for i, attempt := range err.Attempts {
		pbError.Attempts[i] = &pb.DownloadAttempt{
			Method:     attempt.Method,
			Interface:  attempt.Interface,
			Error:      attempt.Error,
			DurationMs: attempt.Duration.Milliseconds(),
			HttpStatus: int32(attempt.HTTPStatus),
		}
	}

	return pbError
}

// searchCustomDirectories searches for firmware images in custom directories.
func (s *firmwareManagementServer) searchCustomDirectories(
	directories []string,
) ([]*firmware.ImageSearchResult, []string) {
	var errors []string
	resolvedDirPaths := make([]string, 0, len(directories))

	// Resolve directory paths and check existence
	for _, dir := range directories {
		var dirPath string
		if filepath.IsAbs(dir) {
			// If it's already an absolute path, use it as-is
			dirPath = dir
		} else {
			// If it's a relative/container path, resolve it
			dirPath = paths.ToHost(dir, s.rootFS)
		}
		if _, err := os.Stat(dirPath); os.IsNotExist(err) {
			errors = append(errors, fmt.Sprintf("directory does not exist: %s", dir))
			continue
		}
		resolvedDirPaths = append(resolvedDirPaths, dirPath)
	}

	// Perform search with resolved paths
	extensions := []string{"*.bin", "*.swi"}
	results, err := firmware.FindAllImagesInDirectories(resolvedDirPaths, extensions)
	if err != nil {
		errors = append(errors, err.Error())
		// Continue with empty results rather than fail
		results = []*firmware.ImageSearchResult{}
	}

	return results, errors
}
