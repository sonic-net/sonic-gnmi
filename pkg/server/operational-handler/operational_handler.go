package operationalhandler

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Value represents a gNMI value with path and data.
// This is a minimal version to avoid importing the full sonic proto package.
type Value struct {
	Path      *gnmipb.Path
	Value     *gnmipb.TypedValue
	Timestamp int64
}

// Handler is the minimal interface required for gNMI handlers.
// This avoids importing the full sonic_data_client package.
type Handler interface {
	StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList)
	Get(w *sync.WaitGroup) ([]*Value, error)
	Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error
	Capabilities() []gnmipb.ModelData
	Close() error
	FailedSend()
}

// OperationalHandler implements the Handler interface for operational state gNMI queries.
// It handles paths like /sonic/system/filesystem[path=*]/disk-space for disk space monitoring
// and other operational data.
type OperationalHandler struct {
	prefix       *gnmipb.Path
	paths        []*gnmipb.Path
	pathHandlers map[string]PathHandler
	mu           sync.RWMutex
}

// PathHandler defines the interface for handling specific gNMI paths.
type PathHandler interface {
	// HandleGet processes a gNMI Get request for this path and returns the response data.
	HandleGet(path *gnmipb.Path) ([]byte, error)

	// SupportedPaths returns the list of paths this handler supports.
	SupportedPaths() []string
}

// NewOperationalHandler creates a new OperationalHandler for the given paths and prefix.
// It follows the same signature as other sonic-gnmi handlers like NewNonDbClient.
func NewOperationalHandler(paths []*gnmipb.Path, prefix *gnmipb.Path) (Handler, error) {
	handler := &OperationalHandler{
		prefix:       prefix,
		paths:        paths,
		pathHandlers: make(map[string]PathHandler),
	}

	// Register path handlers
	diskSpaceHandler := NewDiskSpaceHandler()
	for _, supportedPath := range diskSpaceHandler.SupportedPaths() {
		handler.pathHandlers[supportedPath] = diskSpaceHandler
	}

	// Register file listing handler if filesystem/files paths are requested
	needsFileHandler := false
	for _, path := range paths {
		pathStr := handler.pathToString(path)
		// Check for both firmware paths (legacy) and filesystem/files paths (new)
		// Must explicitly contain "/files/" or "/files[" to avoid matching "/filesystem"
		if strings.Contains(pathStr, "/firmware/") ||
			strings.Contains(pathStr, "/firmware[") ||
			strings.HasSuffix(pathStr, "/firmware") ||
			strings.Contains(pathStr, "/files/") ||
			strings.Contains(pathStr, "/files[") ||
			strings.HasSuffix(pathStr, "/files") {
			needsFileHandler = true
			break
		}
	}

	if needsFileHandler {
		fileHandler := NewFirmwareHandler() // Still using FirmwareHandler internally
		for _, supportedPath := range fileHandler.SupportedPaths() {
			handler.pathHandlers[supportedPath] = fileHandler
		}
	}

	// Validate that all requested paths are supported
	for _, path := range paths {
		pathStr := handler.pathToString(path)
		if !handler.isPathSupported(pathStr) {
			return nil, status.Errorf(codes.Unimplemented, "unsupported path: %s", pathStr)
		}
	}

	return handler, nil
}

// isPathSupported checks if a path string is supported by any registered handler.
func (h *OperationalHandler) isPathSupported(pathStr string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Check exact matches first
	if _, exists := h.pathHandlers[pathStr]; exists {
		return true
	}

	// Check pattern matches for paths with keys like [path=*]
	for supportedPath := range h.pathHandlers {
		if h.pathMatches(pathStr, supportedPath) {
			return true
		}
	}

	return false
}

// pathMatches checks if a requested path matches a supported path pattern.
func (h *OperationalHandler) pathMatches(requestedPath, supportedPath string) bool {
	// Simple pattern matching for now - can be enhanced for more complex patterns
	// For now, handle the case where supportedPath contains [path=*] wildcard
	if supportedPath == "filesystem/disk-space" {
		// Match paths like "filesystem[path=*]/disk-space"
		suffix := "/disk-space"
		if requestedPath == "filesystem/disk-space" {
			return true
		}
		// Check if requestedPath ends with the suffix (safely handle short paths)
		if len(requestedPath) >= len(suffix) && requestedPath[len(requestedPath)-len(suffix):] == suffix {
			return true
		}
		return false
	}

	if supportedPath == "filesystem/files" {
		// Match paths like "filesystem[path=*]/files", "filesystem[path=*]/files[pattern=*]/list", etc.
		if requestedPath == "filesystem/files" {
			return true
		}
		// Check if requestedPath contains filesystem/files pattern
		if strings.Contains(requestedPath, "filesystem") && strings.Contains(requestedPath, "/files") {
			return true
		}
		return false
	}

	// Legacy support for firmware paths (deprecated, use filesystem/files instead)
	if supportedPath == "firmware/files" {
		// Match paths like "firmware[directory=*]/files", "firmware[directory=*]/files/count", etc.
		if requestedPath == "firmware/files" {
			return true
		}
		// Check if requestedPath contains firmware/files pattern
		if strings.Contains(requestedPath, "firmware") && strings.Contains(requestedPath, "/files") {
			return true
		}
		return false
	}

	return requestedPath == supportedPath
}

// pathToString converts a gNMI Path to a string representation.
func (h *OperationalHandler) pathToString(path *gnmipb.Path) string {
	if path == nil {
		return ""
	}

	var pathStr string
	for i, elem := range path.GetElem() {
		if i > 0 {
			pathStr += "/"
		}
		pathStr += elem.GetName()
		// Add key information if present
		if len(elem.GetKey()) > 0 {
			pathStr += "["
			first := true
			for key, value := range elem.GetKey() {
				if !first {
					pathStr += ","
				}
				pathStr += key + "=" + value
				first = false
			}
			pathStr += "]"
		}
	}
	return pathStr
}

// Get implements the Handler interface Get method.
func (h *OperationalHandler) Get(w *sync.WaitGroup) ([]*Value, error) {
	// Capture timestamp at the beginning of the Get operation
	ts := time.Now().UnixNano()

	// Build list of (path, handler) pairs while holding the lock
	type pathHandlerPair struct {
		path    *gnmipb.Path
		handler PathHandler
	}
	var pairs []pathHandlerPair

	h.mu.RLock()
	for _, path := range h.paths {
		pathStr := h.pathToString(path)

		// Find the appropriate handler
		var handler PathHandler
		if ph, exists := h.pathHandlers[pathStr]; exists {
			handler = ph
		} else {
			// Try pattern matching
			for supportedPath, ph := range h.pathHandlers {
				if h.pathMatches(pathStr, supportedPath) {
					handler = ph
					break
				}
			}
		}

		if handler == nil {
			h.mu.RUnlock()
			return nil, status.Errorf(codes.Unimplemented, "no handler found for path: %s", pathStr)
		}

		pairs = append(pairs, pathHandlerPair{path: path, handler: handler})
	}
	h.mu.RUnlock()

	// Now perform I/O operations without holding the lock
	var values []*Value
	for _, pair := range pairs {
		// Get data from the handler (may involve disk I/O)
		data, err := pair.handler.HandleGet(pair.path)
		if err != nil {
			pathStr := h.pathToString(pair.path)
			// Wrap the underlying error with NotFound - most errors are path-related
			return nil, status.Errorf(codes.NotFound, "failed to get data for path %s: %v", pathStr, err)
		}

		// Create Value from the data
		value := &Value{
			Path:      pair.path,
			Value:     &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonVal{JsonVal: data}},
			Timestamp: ts,
		}

		values = append(values, value)
	}

	return values, nil
}

// StreamRun implements the Handler interface StreamRun method (streaming subscriptions).
// For now, this is a stub implementation as operational queries typically use Get requests.
func (h *OperationalHandler) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// TODO: Implement streaming support if needed for operational queries
	// For now, operational queries are primarily Get-based

	select {
	case <-stop:
		return
	}
}

// PollRun implements the Handler interface PollRun method.
func (h *OperationalHandler) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// TODO: Implement poll support if needed
	select {
	case <-poll:
		return
	}
}

// AppDBPollRun implements the Handler interface AppDBPollRun method.
func (h *OperationalHandler) AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// Not applicable for operational handler
	select {
	case <-poll:
		return
	}
}

// OnceRun implements the Handler interface OnceRun method.
func (h *OperationalHandler) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// TODO: Implement once support if needed
	select {
	case <-once:
		return
	}
}

// Set implements the Handler interface Set method.
// Operational queries are primarily read-only, so this returns an error.
func (h *OperationalHandler) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return fmt.Errorf("set operations not supported on operational paths")
}

// Capabilities implements the Handler interface Capabilities method.
func (h *OperationalHandler) Capabilities() []gnmipb.ModelData {
	// Return empty slice for now - operational queries don't require specific YANG models
	return []gnmipb.ModelData{}
}

// Close implements the Handler interface Close method.
func (h *OperationalHandler) Close() error {
	// No resources to clean up for the operational handler
	return nil
}

// FailedSend implements the Handler interface FailedSend method.
func (h *OperationalHandler) FailedSend() {
	// No-op for operational handler
}
