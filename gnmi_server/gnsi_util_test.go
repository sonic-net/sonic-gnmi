package gnmi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp" // Corrected import path for external builds
)

// --- Test copyFile ---
func TestCopyFile(t *testing.T) {
	tests := []struct {
		name           string
		srcContent     string
		srcFile        string
		dstFile        string
		makeSrc        bool
		makeSrcDir     bool
		wantErr        bool
		wantDstContent string
		wantDstMode    os.FileMode
	}{
		{
			name:           "Success",
			srcContent:     "test content",
			srcFile:        "src.txt",
			dstFile:        "dst.txt",
			makeSrc:        true,
			wantErr:        false,
			wantDstContent: "test content",
			wantDstMode:    0600,
		},
		{
			name:    "SrcNotExist",
			srcFile: "nonexistent.txt",
			dstFile: "dst.txt",
			makeSrc: false,
			wantErr: true,
		},
		{
			name:       "SrcIsNotRegularFile",
			srcFile:    "srcDir",
			dstFile:    "dst.txt",
			makeSrc:    true,
			makeSrcDir: true,
			wantErr:    true,
		},
		{
			name:       "DstParentNotExist",
			srcContent: "test",
			srcFile:    "src.txt",
			dstFile:    "subdir/dst.txt",
			makeSrc:    true,
			wantErr:    true, // Expect error because "subdir" doesn't exist
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subTmpDir := t.TempDir() // Create a unique temp directory for each subtest

			srcPath := filepath.Join(subTmpDir, tt.srcFile)
			dstPath := filepath.Join(subTmpDir, tt.dstFile)

			if tt.makeSrc {
				if tt.makeSrcDir {
					if err := os.MkdirAll(srcPath, 0755); err != nil {
						t.Fatalf("Failed to create src directory: %v", err)
					}
				} else {
					if err := os.MkdirAll(filepath.Dir(srcPath), 0755); err != nil {
						t.Fatalf("Failed to create parent dir for src: %v", err)
					}
					if err := os.WriteFile(srcPath, []byte(tt.srcContent), 0644); err != nil {
						t.Fatalf("Failed to create src file: %v", err)
					}
				}
			}

			// This block ensures the destination parent exists ONLY for cases where no error is expected.
			if !tt.wantErr {
				dstParent := filepath.Dir(dstPath)
				if dstParent != subTmpDir {
					if err := os.MkdirAll(dstParent, 0755); err != nil {
						t.Fatalf("Failed to create parent dir for dst: %v", err)
					}
				}
			}

			err := copyFile(srcPath, dstPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("copyFile(%q, %q) got nil error, want error", tt.srcFile, tt.dstFile)
				}
				// Verify destination file was not created. os.Stat should return an error.
				if _, statErr := os.Stat(dstPath); statErr == nil {
					t.Errorf("copyFile(%q, %q) destination file exists, but should not on error", tt.srcFile, tt.dstFile)
				}
			} else {
				if err != nil {
					t.Errorf("copyFile(%q, %q) got error %v, want nil", tt.srcFile, tt.dstFile, err)
				}
				gotContent, err := os.ReadFile(dstPath)
				if err != nil {
					t.Fatalf("Failed to read destination file: %v", err)
				}
				if diff := cmp.Diff(tt.wantDstContent, string(gotContent)); diff != "" {
					t.Errorf("copyFile(%q, %q) content mismatch (-want +got):\n%s", tt.srcFile, tt.dstFile, diff)
				}
				stat, err := os.Stat(dstPath)
				if err != nil {
					t.Fatalf("Failed to stat destination file: %v", err)
				}
				if got, want := stat.Mode().Perm(), os.FileMode(0600); got != want {
					t.Errorf("copyFile(%q, %q) got permissions %v, want %v", tt.srcFile, tt.dstFile, got, want)
				}
			}
		})
	}
}

// --- Test fileCheck ---
func TestFileCheck(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		setup    func(string, string) error // Function to set up the file/dir, takes tmpDir and filePath
		wantErr  bool
	}{
		{
			name:     "RegularFile",
			filePath: "regular.txt",
			setup:    func(tmp, p string) error { return os.WriteFile(p, []byte("test"), 0644) },
			wantErr:  false,
		},
		{
			name:     "NonExistentFile",
			filePath: "nonexistent.txt",
			setup:    func(tmp, p string) error { return nil },
			wantErr:  true,
		},
		{
			name:     "IsDirectory",
			filePath: "a_directory",
			setup:    func(tmp, p string) error { return os.Mkdir(p, 0755) },
			wantErr:  true,
		},
		{
			name:     "SymlinkToFile",
			filePath: "link.txt",
			setup: func(tmp, p string) error {
				targetPath := filepath.Join(tmp, "target.txt")
				if err := os.WriteFile(targetPath, []byte("data"), 0644); err != nil {
					return err
				}
				return os.Symlink(targetPath, p)
			},
			wantErr: true, // os.Lstat will show this is a symlink, not a regular file.
		},
		{
			name:     "SymlinkToDir",
			filePath: "link_to_dir",
			setup: func(tmp, p string) error {
				targetPath := filepath.Join(tmp, "target_dir")
				if err := os.Mkdir(targetPath, 0755); err != nil {
					return err
				}
				return os.Symlink(targetPath, p)
			},
			wantErr: true, // os.Lstat will show this is a symlink, not a regular file.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subTmpDir := t.TempDir()
			fullPath := filepath.Join(subTmpDir, tt.filePath)

			if tt.setup != nil {
				if err := tt.setup(subTmpDir, fullPath); err != nil {
					t.Fatalf("Failed to setup test case: %v", err)
				}
			}

			err := fileCheck(fullPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("fileCheck(%q) got nil error, want error", tt.filePath)
				}
			} else {
				if err != nil {
					t.Errorf("fileCheck(%q) got error %v, want nil", tt.filePath, err)
				}
			}
		})
	}
}
