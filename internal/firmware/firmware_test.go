package firmware

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	monitor := New()
	assert.NotNil(t, monitor)
}

func TestIsFirmwareFile(t *testing.T) {
	testCases := []struct {
		filename string
		expected bool
		reason   string
	}{
		// Firmware files (should return true)
		{"device-driver.bin", true, "Device firmware with .bin extension"},
		{"bootloader.bin", true, "Bootloader with .bin extension"},
		{"network-adapter.bin", true, "Network adapter firmware"},
		{"switch-firmware.bin", true, "Switch firmware file"},
		{"driver1.bin", true, "Generic driver firmware"},
		{"asic-firmware.bin", true, "ASIC firmware with .bin extension"},
		{"fpga-bitstream.bin", true, "FPGA bitstream firmware"},
		{"generic-system.img", true, "Generic .img file (not SONIC OS)"},

		// Files with firmware keywords but unsupported extensions (should return false)
		{"firmware-update.txt", false, "File with 'firmware' keyword but unsupported extension"},
		{"driver-config.json", false, "File with 'driver' keyword but unsupported extension"},
		{"bootloader-settings.conf", false, "File with 'bootloader' keyword but unsupported extension"},

		// Files with unsupported extensions (should return false)
		{"bootloader.fw", false, "Firmware file with .fw extension (not supported)"},
		{"microcode.hex", false, "Microcode with .hex extension (not supported)"},
		{"bios-update.rom", false, "BIOS firmware with .rom extension (not supported)"},
		{"phy-driver.ucode", false, "PHY driver microcode (not supported)"},

		// SONIC OS images (should return false - these belong to sonic-image package)
		{"sonic-vs.4.0.0.img", false, "SONIC OS image (handled by sonic-image package)"},
		{"sonic-broadcom.3.5.2.iso", false, "SONIC OS ISO image"},
		{"SONIC-mellanox.qcow2", false, "SONIC OS VM image"},

		// Other system files (should return false)
		{"README.txt", false, "Documentation file"},
		{"config.json", false, "Configuration file"},
		{"log.txt", false, "Log file"},
		{"system.tar.gz", false, "Archive file"},
		{"application.exe", false, "Application executable"},

		// Edge cases
		{"", false, "Empty filename"},
		{"firmware", false, "Just 'firmware' in filename but no supported extension"},
		{"DRIVER.BIN", true, "All caps firmware file"},
		{"generic-system.img", true, "Generic .img file (not SONIC OS)"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			result := IsFirmwareFile(tc.filename)
			assert.Equal(t, tc.expected, result, "Failed for %s: %s", tc.filename, tc.reason)
		})
	}
}

func TestDetermineFirmwareType(t *testing.T) {
	testCases := []struct {
		filename string
		isDir    bool
		expected string
		reason   string
	}{
		// Directories
		{"drivers", true, "directory", "Directory should be typed as directory"},

		// Specific firmware types
		{"bootloader.bin", false, "bootloader", "Bootloader firmware"},
		{"boot-firmware.bin", false, "bootloader", "Boot firmware (changed to .bin)"},
		{"bios-update.bin", false, "bios", "BIOS firmware (changed to .bin)"},
		{"uefi-firmware.bin", false, "bios", "UEFI firmware"},
		{"network-driver.bin", false, "driver", "Network driver"},
		{"microcode.bin", false, "microcode", "Microcode file (changed to .bin)"},
		{"cpu-ucode.bin", false, "microcode", "CPU microcode (changed to .bin)"},
		{"asic-firmware.bin", false, "asic", "ASIC firmware"},
		{"fpga-bitstream.bin", false, "asic", "FPGA bitstream"},
		{"phy-driver.bin", false, "phy", "PHY driver (phy keyword is more specific)"},
		{"serdes-config.bin", false, "phy", "SerDes configuration"},

		// Generic firmware files
		{"device.bin", false, "firmware", "Generic .bin firmware"},
		{"system.img", false, "firmware", "Generic .img firmware (not SONIC OS)"},

		// Non-firmware files
		{"config.txt", false, "other", "Configuration file"},
		{"readme.md", false, "other", "Documentation file"},
		{"sonic-vs.img", false, "other", "SONIC OS image (not firmware)"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			result := determineFirmwareType(tc.filename, tc.isDir)
			assert.Equal(t, tc.expected, result, "Failed for %s: %s", tc.filename, tc.reason)
		})
	}
}

func TestMonitor_ListFiles(t *testing.T) {
	monitor := New()

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
		"asic-firmware.bin":  "ASIC firmware binary",
		"config.txt":         "configuration file",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(firmwareDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		require.NoError(t, err)
	}

	// Create subdirectory with nested files
	driversDir := filepath.Join(firmwareDir, "drivers")
	err = os.MkdirAll(driversDir, 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(driversDir, "phy-driver.bin"), []byte("PHY driver"), 0644)
	require.NoError(t, err)

	// Test listing files
	files, err := monitor.ListFiles(firmwareDir, "")
	require.NoError(t, err)
	assert.Len(t, files, 7) // 5 files + 1 directory + 1 nested file

	// Verify file types are correctly determined
	fileTypes := make(map[string]string)
	for _, file := range files {
		fileTypes[file.Name] = file.Type
	}

	assert.Equal(t, "bootloader", fileTypes["bootloader.bin"])
	assert.Equal(t, "driver", fileTypes["network-driver.bin"])
	assert.Equal(t, "microcode", fileTypes["microcode.bin"])
	assert.Equal(t, "asic", fileTypes["asic-firmware.bin"])
	assert.Equal(t, "other", fileTypes["config.txt"])
	assert.Equal(t, "directory", fileTypes["drivers"])
	assert.Equal(t, "phy", fileTypes["drivers/phy-driver.bin"])
}

func TestMonitor_GetFileCount(t *testing.T) {
	monitor := New()

	// Create temporary directory with files
	tempDir := t.TempDir()
	firmwareDir := filepath.Join(tempDir, "firmware")
	err := os.MkdirAll(firmwareDir, 0755)
	require.NoError(t, err)

	// Create test files
	for i := 0; i < 3; i++ {
		filename := filepath.Join(firmwareDir, fmt.Sprintf("firmware%d.bin", i))
		err := os.WriteFile(filename, []byte("test content"), 0644)
		require.NoError(t, err)
	}

	count, err := monitor.GetFileCount(firmwareDir, "")
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

func TestMonitor_GetFileInfo(t *testing.T) {
	monitor := New()

	// Create temporary directory with a test file
	tempDir := t.TempDir()
	firmwareDir := filepath.Join(tempDir, "firmware")
	err := os.MkdirAll(firmwareDir, 0755)
	require.NoError(t, err)

	testContent := "test firmware content"
	testFile := filepath.Join(firmwareDir, "test-firmware.bin")
	err = os.WriteFile(testFile, []byte(testContent), 0644)
	require.NoError(t, err)

	// Test getting specific file info
	fileInfo, err := monitor.GetFileInfo(firmwareDir, "test-firmware.bin", "")
	require.NoError(t, err)
	require.NotNil(t, fileInfo)

	assert.Equal(t, "test-firmware.bin", fileInfo.Name)
	assert.Equal(t, int64(len(testContent)), fileInfo.Size)
	assert.Equal(t, "firmware", fileInfo.Type)
	assert.False(t, fileInfo.IsDirectory)
	assert.NotEmpty(t, fileInfo.Permissions)
	assert.False(t, fileInfo.ModTime.IsZero())
}

func TestMonitor_GetFilesByType(t *testing.T) {
	monitor := New()

	// Create temporary directory structure
	tempDir := t.TempDir()
	firmwareDir := filepath.Join(tempDir, "firmware")
	err := os.MkdirAll(firmwareDir, 0755)
	require.NoError(t, err)

	// Create different types of firmware files
	testFiles := map[string]string{
		"bootloader.bin": "bootloader content",
		"driver1.bin":    "driver content",
		"driver2.bin":    "driver content",
		"microcode.bin":  "microcode content",
		"config.txt":     "config content",
	}

	for filename, content := range testFiles {
		filePath := filepath.Join(firmwareDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		require.NoError(t, err)
	}

	// Test filtering by driver type
	driverFiles, err := monitor.GetFilesByType(firmwareDir, "", "driver")
	require.NoError(t, err)
	assert.Len(t, driverFiles, 2) // driver1.bin and driver2.bin

	// Test getting all files
	allFiles, err := monitor.GetFilesByType(firmwareDir, "", "all")
	require.NoError(t, err)
	assert.Len(t, allFiles, 5) // All test files

	// Test filtering by bootloader type
	bootloaderFiles, err := monitor.GetFilesByType(firmwareDir, "", "bootloader")
	require.NoError(t, err)
	assert.Len(t, bootloaderFiles, 1) // Only bootloader.bin
	assert.Equal(t, "bootloader.bin", bootloaderFiles[0].Name)
}

func TestMonitor_ErrorCases(t *testing.T) {
	monitor := New()

	// Test non-existent directory
	_, err := monitor.ListFiles("/non/existent/path", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")

	// Test non-existent file
	tempDir := t.TempDir()
	_, err = monitor.GetFileInfo(tempDir, "nonexistent.bin", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "file not found")
}

func TestFormatFileInfo(t *testing.T) {
	// Test with nil info
	result := FormatFileInfo(nil)
	assert.Equal(t, "No firmware file information available", result)

	// Test with file info
	fileInfo := &FileInfo{
		Name:        "test-firmware.bin",
		Size:        1024,
		ModTime:     time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		IsDirectory: false,
		Permissions: "-rw-r--r--",
		Type:        "firmware",
	}

	result = FormatFileInfo(fileInfo)
	assert.Contains(t, result, "test-firmware.bin")
	assert.Contains(t, result, "1024 bytes")
	assert.Contains(t, result, "[firmware]")
	assert.Contains(t, result, "-rw-r--r--")

	// Test with file info including MD5
	fileInfoWithMD5 := &FileInfo{
		Name:        "test-firmware.bin",
		Size:        1024,
		ModTime:     time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
		IsDirectory: false,
		Permissions: "-rw-r--r--",
		Type:        "firmware",
		MD5Sum:      "5d41402abc4b2a76b9719d911017c592",
	}

	result = FormatFileInfo(fileInfoWithMD5)
	assert.Contains(t, result, "test-firmware.bin")
	assert.Contains(t, result, "1024 bytes")
	assert.Contains(t, result, "[firmware]")
	assert.Contains(t, result, "-rw-r--r--")
	assert.Contains(t, result, "MD5: 5d41402abc4b2a76b9719d911017c592")

	// Test with directory info
	dirInfo := &FileInfo{
		Name:        "drivers",
		IsDirectory: true,
		Permissions: "drwxr-xr-x",
		Type:        "directory",
		ModTime:     time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC),
	}

	result = FormatFileInfo(dirInfo)
	assert.Contains(t, result, "Directory: drivers")
	assert.Contains(t, result, "[directory]")
	assert.Contains(t, result, "drwxr-xr-x")
	assert.NotContains(t, result, "MD5:")
}

func TestCalculateMD5(t *testing.T) {
	// Create a temporary file with known content
	tempDir := t.TempDir()
	testFile := filepath.Join(tempDir, "test.bin")

	// Write "hello" to the file - MD5 should be 5d41402abc4b2a76b9719d911017c592
	err := os.WriteFile(testFile, []byte("hello"), 0644)
	require.NoError(t, err)

	// Calculate MD5
	md5sum, err := calculateMD5(testFile)
	require.NoError(t, err)
	assert.Equal(t, "5d41402abc4b2a76b9719d911017c592", md5sum)

	// Test with non-existent file
	_, err = calculateMD5("/nonexistent/file.bin")
	assert.Error(t, err)
}
