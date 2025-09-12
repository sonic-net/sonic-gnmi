package upgradehandler

import (
	"fmt"
	"sync"

	"github.com/Workiva/go-datastructures/queue"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// Value represents a gNMI value with path and data.
// This is a minimal version to avoid importing the full sonic proto package.
type Value struct {
	Path      *gnmipb.Path
	Value     *gnmipb.TypedValue
	Timestamp int64
}

// Client is the minimal interface required for gNMI handlers.
// This avoids importing the full sonic_data_client package.
type Client interface {
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

// UpgradeHandler implements the Client interface for upgrade-related gNMI operations.
// It handles paths like /sonic/system/filesystem[path=*]/disk-space for disk space monitoring
// and other upgrade-related functionality.
type UpgradeHandler struct {
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

// NewUpgradeHandler creates a new UpgradeHandler for the given paths and prefix.
// It follows the same signature as other sonic-gnmi clients like NewNonDbClient.
func NewUpgradeHandler(paths []*gnmipb.Path, prefix *gnmipb.Path) (Client, error) {
	handler := &UpgradeHandler{
		prefix:       prefix,
		paths:        paths,
		pathHandlers: make(map[string]PathHandler),
	}

	// Register path handlers
	diskSpaceHandler := NewDiskSpaceHandler()
	for _, supportedPath := range diskSpaceHandler.SupportedPaths() {
		handler.pathHandlers[supportedPath] = diskSpaceHandler
	}

	// Validate that all requested paths are supported
	for _, path := range paths {
		pathStr := handler.pathToString(path)
		if !handler.isPathSupported(pathStr) {
			return nil, fmt.Errorf("unsupported path: %s", pathStr)
		}
	}

	return handler, nil
}

// isPathSupported checks if a path string is supported by any registered handler.
func (h *UpgradeHandler) isPathSupported(pathStr string) bool {
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
func (h *UpgradeHandler) pathMatches(requestedPath, supportedPath string) bool {
	// Simple pattern matching for now - can be enhanced for more complex patterns
	// For now, handle the case where supportedPath contains [path=*] wildcard
	if supportedPath == "filesystem/disk-space" {
		// Match paths like "filesystem[path=*]/disk-space"
		return requestedPath == "filesystem/disk-space" ||
			requestedPath[len(requestedPath)-len("/disk-space"):] == "/disk-space"
	}
	return requestedPath == supportedPath
}

// pathToString converts a gNMI Path to a string representation.
func (h *UpgradeHandler) pathToString(path *gnmipb.Path) string {
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

// Get implements the Client interface Get method.
func (h *UpgradeHandler) Get(w *sync.WaitGroup) ([]*Value, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var values []*Value

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
			return nil, fmt.Errorf("no handler found for path: %s", pathStr)
		}

		// Get data from the handler
		data, err := handler.HandleGet(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get data for path %s: %v", pathStr, err)
		}

		// Create Value from the data
		value := &Value{
			Path:      path,
			Value:     &gnmipb.TypedValue{Value: &gnmipb.TypedValue_JsonVal{JsonVal: data}},
			Timestamp: 0, // Will be set by the server
		}

		values = append(values, value)
	}

	return values, nil
}

// StreamRun implements the Client interface StreamRun method (streaming subscriptions).
// For now, this is a stub implementation as upgrade operations typically use Get requests.
func (h *UpgradeHandler) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// TODO: Implement streaming support if needed for upgrade operations
	// For now, upgrade operations are primarily Get-based

	select {
	case <-stop:
		return
	}
}

// PollRun implements the Client interface PollRun method.
func (h *UpgradeHandler) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// TODO: Implement poll support if needed
	select {
	case <-poll:
		return
	}
}

// AppDBPollRun implements the Client interface AppDBPollRun method.
func (h *UpgradeHandler) AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// Not applicable for upgrade handler
	select {
	case <-poll:
		return
	}
}

// OnceRun implements the Client interface OnceRun method.
func (h *UpgradeHandler) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	defer w.Done()

	// TODO: Implement once support if needed
	select {
	case <-once:
		return
	}
}

// Set implements the Client interface Set method.
// Upgrade operations are primarily read-only, so this returns an error.
func (h *UpgradeHandler) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return fmt.Errorf("set operations not supported on upgrade paths")
}

// Capabilities implements the Client interface Capabilities method.
func (h *UpgradeHandler) Capabilities() []gnmipb.ModelData {
	// Return empty slice for now - upgrade operations don't require specific YANG models
	return []gnmipb.ModelData{}
}

// Close implements the Client interface Close method.
func (h *UpgradeHandler) Close() error {
	// No resources to clean up for the upgrade handler
	return nil
}

// FailedSend implements the Client interface FailedSend method.
func (h *UpgradeHandler) FailedSend() {
	// No-op for upgrade handler
}
