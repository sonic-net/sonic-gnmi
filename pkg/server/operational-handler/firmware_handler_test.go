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

	expected := []string{"firmware/files"}
	assert.Equal(t, expected, paths)
}

func TestFirmwareHandler_ExtractFirmwarePathInfo(t *testing.T) {
	handler := NewFirmwareHandler()

	tests := []struct {
		name          string
		path          *gnmipb.Path
		expectedDir   string
		expectedField string
		wantErr       bool
	}{
		{
			name: "valid path with firmware directory",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "firmware",
						Key:  map[string]string{"directory": "/lib/firmware"},
					},
					{Name: "files"},
				},
			},
			expectedDir:   "/lib/firmware",
			expectedField: "list",
			wantErr:       false,
		},
		{
			name: "path with count field",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "firmware",
						Key:  map[string]string{"directory": "/lib/firmware"},
					},
					{Name: "files"},
					{Name: "count"},
				},
			},
			expectedDir:   "/lib/firmware",
			expectedField: "count",
			wantErr:       false,
		},
		{
			name: "path with type filter",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "firmware",
						Key:  map[string]string{"directory": "/lib/firmware"},
					},
					{
						Name: "files",
						Key:  map[string]string{"type": "driver"},
					},
				},
			},
			expectedDir:   "/lib/firmware",
			expectedField: "type:driver",
			wantErr:       false,
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
			name: "no firmware element",
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
			name: "firmware element without directory key",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "firmware",
						Key:  map[string]string{"other": "value"},
					},
					{Name: "files"},
				},
			},
			wantErr: true,
		},
		{
			name: "firmware element with empty directory",
			path: &gnmipb.Path{
				Elem: []*gnmipb.PathElem{
					{Name: "sonic"},
					{Name: "system"},
					{
						Name: "firmware",
						Key:  map[string]string{"directory": ""},
					},
					{Name: "files"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, field, err := handler.extractFirmwarePathInfo(tt.path)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedDir, dir)
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

	t.Run("list all firmware files", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "firmware",
					Key:  map[string]string{"directory": firmwareDir},
				},
				{Name: "files"},
			},
		}

		data, err := handler.HandleGet(path)
		require.NoError(t, err)

		var response map[string]interface{}
		err = json.Unmarshal(data, &response)
		require.NoError(t, err)

		assert.Equal(t, firmwareDir, response["directory"])
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

	t.Run("get firmware file count", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "firmware",
					Key:  map[string]string{"directory": firmwareDir},
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

	t.Run("get specific firmware file info", func(t *testing.T) {
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "firmware",
					Key:  map[string]string{"directory": firmwareDir},
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

		assert.Equal(t, firmwareDir, response["directory"])

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
					Name: "firmware",
					Key:  map[string]string{"directory": firmwareDir},
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

		assert.Equal(t, firmwareDir, response["directory"])

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
					Name: "firmware",
					Key:  map[string]string{"directory": "/non/existent/path"},
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
					Name: "firmware",
					Key:  map[string]string{"directory": tempDir},
				},
				{Name: "files"},
				{Name: "nonexistent.bin"},
			},
		}

		_, err := handler.HandleGet(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported firmware field")
	})

	t.Run("unsupported field", func(t *testing.T) {
		tempDir := t.TempDir()
		path := &gnmipb.Path{
			Elem: []*gnmipb.PathElem{
				{Name: "sonic"},
				{Name: "system"},
				{
					Name: "firmware",
					Key:  map[string]string{"directory": tempDir},
				},
				{Name: "files"},
				{Name: "unsupported"},
			},
		}

		_, err := handler.HandleGet(path)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported firmware field")
	})
}
