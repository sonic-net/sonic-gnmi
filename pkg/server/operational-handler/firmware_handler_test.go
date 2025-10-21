package operationalhandler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFirmwareHandler_SupportedPaths(t *testing.T) {
	handler := NewFirmwareHandler()
	paths := handler.SupportedPaths()

	expected := []string{"filesystem/files"}
	assert.Equal(t, expected, paths)
}

func TestFirmwareHandler_ExtractFilePathInfo(t *testing.T) {
	handler := NewFirmwareHandler()

	tests := []struct {
		name            string
		path            *gnmipb.Path
		expectedPath    string
		expectedPattern string
		expectedField   string
		wantErr         bool
	}{
		{
			name: "valid path with filesystem path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/tmp"},
					},
					{Name: "files"},
				},
			},
			expectedPath:    "/tmp",
			expectedPattern: "*",
			expectedField:   "list",
			wantErr:         false,
		},
		{
			name: "path with pattern filter",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/tmp"},
					},
					{
						Name: "files",
						Key:  map[string]string{"pattern": "*.bin"},
					},
					{Name: "list"},
				},
			},
			expectedPath:    "/tmp",
			expectedPattern: "*.bin",
			expectedField:   "list",
			wantErr:         false,
		},
		{
			name: "path with count field",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": "/tmp"},
					},
					{
						Name: "files",
						Key:  map[string]string{"pattern": "*.bin"},
					},
					{Name: "count"},
				},
			},
			expectedPath:    "/tmp",
			expectedPattern: "*.bin",
			expectedField:   "count",
			wantErr:         false,
		},
		{
			name:    "nil path",
			path:    nil,
			wantErr: true,
		},
		{
			name: "empty path elements",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{},
			},
			wantErr: true,
		},
		{
			name: "no filesystem element",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{Name: "files"},
				},
			},
			wantErr: true,
		},
		{
			name: "filesystem element without path key",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"other": "value"},
					},
					{Name: "files"},
				},
			},
			wantErr: true,
		},
		{
			name: "filesystem element with empty path",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "filesystem",
						Key:  map[string]string{"path": ""},
					},
					{Name: "files"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, pattern, field, err := handler.extractFilePathInfo(tt.path)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPath, path)
				assert.Equal(t, tt.expectedPattern, pattern)
				assert.Equal(t, tt.expectedField, field)
			}
		})
	}
}

func TestFirmwareHandler_HandleGet(t *testing.T) {
	handler := NewFirmwareHandler()

	// Create temporary directory structure
	tempDir := t.TempDir()
	firmwareDir := filepath.Join(tempDir, "firmware")
	err := os.MkdirAll(firmwareDir, 0755)
	require.NoError(t, err)

	// Create test firmware files
	testFiles := map[string]string{
		"bootloader.bin":     "bootloader firmware content",
		"network-driver.bin": "network driver firmware",
		"microcode.bin":      "microcode data",
		"config.txt":         "configuration file",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(firmwareDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		require.NoError(t, err)
	}

	t.Run("list all files", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": firmwareDir},
				},
				{Name: "files"},
			},
		}

		data, err := handler.HandleGet(path)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(data, &response)
		require.NoError(t, err)

		assert.Equal(t, firmwareDir, response["path"])
		assert.Equal(t, "*", response["pattern"])
		assert.Equal(t, float64(4), response["file_count"]) // JSON unmarshals numbers as float64

		files, ok := response["files"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, files, 4)

		// Verify file properties
		fileNames := make([]string, len(files))
		for i, fileInterface := range files {
			file, ok := fileInterface.(map[string]interface{})
			assert.True(t, ok)

			name, ok := file["name"].(string)
			assert.True(t, ok)
			fileNames[i] = name

			// Check that required fields are present
			assert.Contains(t, file, "size")
			assert.Contains(t, file, "mod_time")
			assert.Contains(t, file, "is_directory")
			assert.Contains(t, file, "permissions")
			assert.Contains(t, file, "type")
		}

		// Check that all expected files are present
		expectedFiles := []string{"bootloader.bin", "network-driver.bin", "microcode.bin", "config.txt"}
		for _, expectedFile := range expectedFiles {
			assert.Contains(t, fileNames, expectedFile)
		}
	})

	t.Run("get file count", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": firmwareDir},
				},
				{Name: "files"},
				{Name: "count"},
			},
		}

		data, err := handler.HandleGet(path)
		require.NoError(t, err)

		var count int
		err = json.Unmarshal(data, &count)
		require.NoError(t, err)
		assert.Equal(t, 4, count)
	})

	t.Run("list files with pattern filter", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": firmwareDir},
				},
				{
					Name: "files",
					Key:  map[string]string{"pattern": "*.bin"},
				},
				{Name: "list"},
			},
		}

		data, err := handler.HandleGet(path)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(data, &response)
		require.NoError(t, err)

		assert.Equal(t, firmwareDir, response["path"])
		assert.Equal(t, "*.bin", response["pattern"])
		assert.Equal(t, float64(3), response["file_count"]) // Only .bin files

		files, ok := response["files"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, files, 3)

		// Verify all files have .bin extension
		for _, fileInterface := range files {
			file, ok := fileInterface.(map[string]interface{})
			assert.True(t, ok)

			name, ok := file["name"].(string)
			assert.True(t, ok)
			assert.Contains(t, name, ".bin")
		}
	})

	t.Run("get specific file info", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": firmwareDir},
				},
				{Name: "files"},
				{Name: "bootloader.bin"},
			},
		}

		data, err := handler.HandleGet(path)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(data, &response)
		require.NoError(t, err)

		assert.Equal(t, firmwareDir, response["path"])

		file, ok := response["file"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "bootloader.bin", file["name"])
		assert.Equal(t, "bootloader", file["type"])
		assert.Equal(t, false, file["is_directory"])
		assert.Equal(t, float64(len("bootloader firmware content")), file["size"])
	})

	t.Run("get files by type", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": firmwareDir},
				},
				{Name: "files"},
				{Name: "types"},
			},
		}

		data, err := handler.HandleGet(path)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(data, &response)
		require.NoError(t, err)

		assert.Equal(t, firmwareDir, response["path"])

		types, ok := response["types"].(map[string]interface{})
		assert.True(t, ok)

		// Check that different firmware types are grouped
		assert.Contains(t, types, "bootloader")
		assert.Contains(t, types, "driver")
		assert.Contains(t, types, "microcode")
		assert.Contains(t, types, "other")
	})
}

func TestFirmwareHandler_GetFirmwareFilesByType(t *testing.T) {
	handler := NewFirmwareHandler()

	// Create temporary directory structure
	tempDir := t.TempDir()
	firmwareDir := filepath.Join(tempDir, "firmware")
	err := os.MkdirAll(firmwareDir, 0755)
	require.NoError(t, err)

	// Create different types of firmware files
	testFiles := map[string]string{
		"bootloader.bin":     "bootloader content",
		"network-driver.bin": "driver content",
		"storage-driver.bin": "driver content",
		"microcode.bin":      "microcode content",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(firmwareDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		require.NoError(t, err)
	}

	// Test filtering by driver type
	driverFiles, err := handler.GetFirmwareFilesByType(firmwareDir, "driver")
	require.NoError(t, err)
	assert.Len(t, driverFiles, 2) // network-driver.bin and storage-driver.bin

	// Verify driver files
	driverNames := make([]string, len(driverFiles))
	for i, file := range driverFiles {
		driverNames[i] = file.Name
		assert.Equal(t, "driver", file.Type)
	}
	assert.Contains(t, driverNames, "network-driver.bin")
	assert.Contains(t, driverNames, "storage-driver.bin")

	// Test filtering by bootloader type
	bootloaderFiles, err := handler.GetFirmwareFilesByType(firmwareDir, "bootloader")
	require.NoError(t, err)
	assert.Len(t, bootloaderFiles, 1)
	assert.Equal(t, "bootloader.bin", bootloaderFiles[0].Name)
	assert.Equal(t, "bootloader", bootloaderFiles[0].Type)
}

func TestFirmwareHandler_IsValidDirectory(t *testing.T) {
	handler := NewFirmwareHandler()

	// Test with valid directory
	tempDir := t.TempDir()
	assert.True(t, handler.IsValidDirectory(tempDir))

	// Test with non-existent directory
	assert.False(t, handler.IsValidDirectory("/non/existent/path"))
}

func TestFirmwareHandler_FormatFirmwareFileInfo(t *testing.T) {
	handler := NewFirmwareHandler()

	// Test with nil info
	result := handler.FormatFirmwareFileInfo(nil)
	assert.Equal(t, "No firmware file information available", result)

	// Test with valid file info
	fileInfo := &FirmwareFileInfo{
		Name:        "test-firmware.bin",
		Size:        1024,
		ModTime:     time.Now().Format("2006-01-02T15:04:05Z07:00"),
		IsDirectory: false,
		Permissions: "-rw-r--r--",
		Type:        "firmware",
	}

	result = handler.FormatFirmwareFileInfo(fileInfo)
	assert.Contains(t, result, "test-firmware.bin")
	assert.Contains(t, result, "1024 bytes")
	assert.Contains(t, result, "[firmware]")
	assert.Contains(t, result, "-rw-r--r--")
}

func TestFirmwareHandler_ErrorCases(t *testing.T) {
	handler := NewFirmwareHandler()

	t.Run("non-existent directory", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": "/non/existent/path"},
				},
				{Name: "files"},
			},
		}

		_, err := handler.HandleGet(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not exist")
	})

	t.Run("non-existent file", func(t *testing.T) {
		tempDir := t.TempDir()
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": tempDir},
				},
				{Name: "files"},
				{Name: "nonexistent.bin"},
			},
		}

		_, err := handler.HandleGet(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported file field")
	})

	t.Run("unsupported field", func(t *testing.T) {
		tempDir := t.TempDir()
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "filesystem",
					Key:  map[string]string{"path": tempDir},
				},
				{Name: "files"},
				{Name: "unsupported"},
			},
		}

		_, err := handler.HandleGet(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported file field")
	})
}
