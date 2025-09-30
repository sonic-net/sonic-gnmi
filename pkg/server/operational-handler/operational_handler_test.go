package operationalhandler

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestNewOperationalHandler(t *testing.T) {
	// Test valid paths
	paths := []*gnmipb.Path{
		{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": "/"},
				},
				{Name: "disk-space"},
			},
		},
	}

	prefix := &gnmipb.Path{
		Target: "OPERATIONAL",
	}

	handler, err := NewOperationalHandler(paths, prefix)
	if err != nil {
		t.Fatalf("failed to create operational handler: %v", err)
	}

	if handler == nil {
		t.Fatal("expected non-nil handler")
	}

	// Verify Close works
	err = handler.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestNewOperationalHandler_UnsupportedPath(t *testing.T) {
	// Test with unsupported path
	paths := []*gnmipb.Path{
		{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "unsupported"},
				{Name: "path"},
			},
		},
	}

	prefix := &gnmipb.Path{
		Target: "OPERATIONAL",
	}

	_, err := NewOperationalHandler(paths, prefix)
	if err == nil {
		t.Fatal("expected error for unsupported path")
	}
}

func TestOperationalHandler_PathToString(t *testing.T) {
	handler := &OperationalHandler{}

	tests := []struct {
		name     string
		path     *gnmipb.Path
		expected string
	}{
		{
			name:     "nil path",
			path:     nil,
			expected: "",
		},
		{
			name: "simple path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
				},
			},
			expected: "sonic/system",
		},
		{
			name: "path with keys",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/", "type": "ext4"},
					},
					{Name: "disk-space"},
				},
			},
			expected: "", // We'll check this differently due to map ordering
		},
		{
			name: "empty elements",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{},
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.pathToString(tt.path)
			if tt.name == "path with keys" {
				// Check that it contains the expected components (order may vary)
				if !strings.Contains(result, "sonic/filesystem[") ||
					!strings.Contains(result, "path=/") ||
					!strings.Contains(result, "type=ext4") ||
					!strings.Contains(result, "]/disk-space") {
					t.Errorf("path with keys missing expected components: %s", result)
				}
			} else if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestOperationalHandler_PathMatches(t *testing.T) {
	handler := &OperationalHandler{}

	tests := []struct {
		name          string
		requestedPath string
		supportedPath string
		expected      bool
	}{
		{
			name:          "exact match",
			requestedPath: "filesystem/disk-space",
			supportedPath: "filesystem/disk-space",
			expected:      true,
		},
		{
			name:          "pattern match with keys",
			requestedPath: "filesystem[path=/]/disk-space",
			supportedPath: "filesystem/disk-space",
			expected:      true,
		},
		{
			name:          "no match",
			requestedPath: "different/path",
			supportedPath: "filesystem/disk-space",
			expected:      false,
		},
		{
			name:          "short path no panic",
			requestedPath: "short",
			supportedPath: "filesystem/disk-space",
			expected:      false,
		},
		{
			name:          "empty path no panic",
			requestedPath: "",
			supportedPath: "filesystem/disk-space",
			expected:      false,
		},
		{
			name:          "path shorter than suffix no panic",
			requestedPath: "/disk",
			supportedPath: "filesystem/disk-space",
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.pathMatches(tt.requestedPath, tt.supportedPath)
			if result != tt.expected {
				t.Errorf("expected %v for paths %s vs %s, got %v",
					tt.expected, tt.requestedPath, tt.supportedPath, result)
			}
		})
	}
}

func TestOperationalHandler_Get(t *testing.T) {
	// Create handler with disk space path
	paths := []*gnmipb.Path{
		{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": "/"},
				},
				{Name: "disk-space"},
			},
		},
	}

	prefix := &gnmipb.Path{
		Target: "OPERATIONAL",
	}

	handler, err := NewOperationalHandler(paths, prefix)
	if err != nil {
		t.Fatalf("failed to create operational handler: %v", err)
	}

	// Cast to OperationalHandler to access Get method
	opHandler, ok := handler.(*OperationalHandler)
	if !ok {
		t.Fatal("expected OperationalHandler type")
	}

	// Test Get method
	values, err := opHandler.Get(nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(values) != 1 {
		t.Fatalf("expected 1 value, got %d", len(values))
	}

	value := values[0]
	if value.Path != paths[0] {
		t.Errorf("path mismatch in response")
	}

	// Verify JSON data
	jsonVal := value.Value.GetJsonVal()
	if jsonVal == nil {
		t.Fatal("expected JSON value")
	}

	var diskSpace DiskSpaceInfo
	err = json.Unmarshal(jsonVal, &diskSpace)
	if err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if diskSpace.Path != "/" {
		t.Errorf("expected path /, got %s", diskSpace.Path)
	}

	if diskSpace.TotalMB == 0 {
		t.Errorf("expected non-zero total MB")
	}
}

func TestOperationalHandler_Set(t *testing.T) {
	handler := &OperationalHandler{}

	// Set operations should not be supported
	err := handler.Set(nil, nil, nil)
	if err == nil {
		t.Error("expected error for Set operation")
	}
}

func TestOperationalHandler_Capabilities(t *testing.T) {
	handler := &OperationalHandler{}

	capabilities := handler.Capabilities()
	// Should return empty slice for now
	if len(capabilities) != 0 {
		t.Errorf("expected empty capabilities, got %d", len(capabilities))
	}
}

func TestOperationalHandler_StreamRun(t *testing.T) {
	handler := &OperationalHandler{}

	var wg sync.WaitGroup
	wg.Add(1)

	stop := make(chan struct{})

	// Start StreamRun in goroutine
	go handler.StreamRun(nil, stop, &wg, nil)

	// Signal stop
	close(stop)

	// Wait for completion
	wg.Wait()

	// Test passes if no panic or deadlock occurs
}

func TestOperationalHandler_PollRun(t *testing.T) {
	handler := &OperationalHandler{}

	var wg sync.WaitGroup
	wg.Add(1)

	poll := make(chan struct{})

	// Start PollRun in goroutine
	go handler.PollRun(nil, poll, &wg, nil)

	// Signal poll
	close(poll)

	// Wait for completion
	wg.Wait()

	// Test passes if no panic or deadlock occurs
}

func TestOperationalHandler_OnceRun(t *testing.T) {
	handler := &OperationalHandler{}

	var wg sync.WaitGroup
	wg.Add(1)

	once := make(chan struct{})

	// Start OnceRun in goroutine
	go handler.OnceRun(nil, once, &wg, nil)

	// Signal once
	close(once)

	// Wait for completion
	wg.Wait()

	// Test passes if no panic or deadlock occurs
}

func TestOperationalHandler_AppDBPollRun(t *testing.T) {
	handler := &OperationalHandler{}

	var wg sync.WaitGroup
	wg.Add(1)

	poll := make(chan struct{})

	// Start AppDBPollRun in goroutine
	go handler.AppDBPollRun(nil, poll, &wg, nil)

	// Signal poll
	close(poll)

	// Wait for completion
	wg.Wait()

	// Test passes if no panic or deadlock occurs
}

func TestOperationalHandler_FailedSend(t *testing.T) {
	handler := &OperationalHandler{}

	// FailedSend should not panic
	handler.FailedSend()

	// Test passes if no panic occurs
}

func TestOperationalHandler_IsPathSupported(t *testing.T) {
	handler := &OperationalHandler{
		pathHandlers: map[string]PathHandler{
			"filesystem/disk-space": NewDiskSpaceHandler(),
		},
	}

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{
			name:     "exact match",
			path:     "filesystem/disk-space",
			expected: true,
		},
		{
			name:     "pattern match",
			path:     "filesystem[path=/]/disk-space",
			expected: true,
		},
		{
			name:     "unsupported path",
			path:     "unsupported/path",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := handler.isPathSupported(tt.path)
			if result != tt.expected {
				t.Errorf("expected %v for path %s, got %v", tt.expected, tt.path, result)
			}
		})
	}
}

// Integration test for the complete workflow
func TestOperationalHandler_Integration(t *testing.T) {
	// Create multiple paths
	paths := []*gnmipb.Path{
		{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": "/"},
				},
				{Name: "disk-space"},
			},
		},
		{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": "/tmp"},
				},
				{Name: "disk-space"},
			},
		},
	}

	prefix := &gnmipb.Path{
		Target: "OPERATIONAL",
	}

	// Create handler
	handler, err := NewOperationalHandler(paths, prefix)
	if err != nil {
		t.Fatalf("failed to create operational handler: %v", err)
	}
	defer handler.Close()

	opHandler := handler.(*OperationalHandler)

	// Get data for all paths
	values, err := opHandler.Get(nil)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}

	// Verify both responses contain valid disk space data
	for i, value := range values {
		jsonVal := value.Value.GetJsonVal()
		if jsonVal == nil {
			t.Fatalf("expected JSON value for path %d", i)
		}

		var diskSpace DiskSpaceInfo
		err = json.Unmarshal(jsonVal, &diskSpace)
		if err != nil {
			t.Fatalf("failed to unmarshal JSON for path %d: %v", i, err)
		}

		if diskSpace.TotalMB == 0 {
			t.Errorf("expected non-zero total MB for path %d", i)
		}

		t.Logf("Path %d: %s -> Total: %d MB, Available: %d MB",
			i, diskSpace.Path, diskSpace.TotalMB, diskSpace.AvailableMB)
	}
}
