package debug

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefaultWhitelists(t *testing.T) {
	read, write := defaultWhitelists()

	if len(read) == 0 {
		t.Fatal("default read whitelist should not be empty")
	}
	if len(write) == 0 {
		t.Fatal("default write whitelist should not be empty")
	}

	writeMap := make(map[string]bool)
	for _, cmd := range write {
		writeMap[cmd] = true
	}

	// Verify every read command is also in the write list
	for _, cmd := range read {
		if !writeMap[cmd] {
			t.Errorf("read command '%s' was not found in the write whitelist", cmd)
		}
	}
}

func TestConstructWhitelists(t *testing.T) {
	defaultRead, defaultWrite := defaultWhitelists()

	testCases := []struct {
		name          string
		fileContent   string // Content to write to the temp file
		createFile    bool   // Flag to control if a file is created (for file-not-found test)
		expectedRead  []string
		expectedWrite []string
	}{
		{
			name:       "Success - Valid Whitelist File",
			createFile: true,
			fileContent: `
read_whitelist:
  - cmd1
  - cmd2
write_whitelist:
  - cmd3
  - cmd4
`,
			expectedRead:  []string{"cmd1", "cmd2"},
			expectedWrite: []string{"cmd3", "cmd4"},
		},
		{
			name:          "Failure - File Not Found",
			createFile:    false, // This will trigger the os.ReadFile error
			fileContent:   "",    // Irrelevant
			expectedRead:  defaultRead,
			expectedWrite: defaultWrite,
		},
		{
			name:          "Failure - YAML Unmarshal Error",
			createFile:    true,
			fileContent:   "read_whitelist: [cmd1, cmd2", // Malformed YAML (missing ']')
			expectedRead:  defaultRead,
			expectedWrite: defaultWrite,
		},
		{
			name:          "Edge Case - Empty File",
			createFile:    true,
			fileContent:   "", // Valid YAML (empty), unmarshals to nil slices
			expectedRead:  defaultRead,
			expectedWrite: defaultWrite,
		},
		{
			name:       "Edge Case - Partially Empty File",
			createFile: true,
			fileContent: `
read_whitelist:
  - cat
  - ls
`, // 'write_whitelist' key is missing, will be a nil slice
			expectedRead:  defaultRead,
			expectedWrite: defaultWrite,
		},
	}

	originalPath := WHITELIST_FILE_PATH
	t.Cleanup(func() {
		WHITELIST_FILE_PATH = originalPath
	})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir := t.TempDir()

			if tc.createFile {
				// Create a temporary file
				tempFile := filepath.Join(tempDir, "whitelist.yaml")
				err := os.WriteFile(tempFile, []byte(tc.fileContent), 0644)
				if err != nil {
					t.Fatalf("Failed to write temp file: %v", err)
				}
				// Point our global var to the temp file
				WHITELIST_FILE_PATH = tempFile
			} else {
				// Point to a file that is guaranteed not to exist
				WHITELIST_FILE_PATH = filepath.Join(tempDir, "non-existent-file.yaml")
			}

			read, write := ConstructWhitelists()

			if !reflect.DeepEqual(read, tc.expectedRead) {
				t.Errorf("Read whitelist mismatch:\ngot:  %v\nwant: %v", read, tc.expectedRead)
			}
			if !reflect.DeepEqual(write, tc.expectedWrite) {
				t.Errorf("Write whitelist mismatch:\ngot:  %v\nwant: %v", write, tc.expectedWrite)
			}
		})
	}
}
