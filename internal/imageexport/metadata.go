package imageexport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

type Metadata struct {
	Path     string      `json:"path"`
	Bytes    int64       `json:"bytes"`
	ModeBits os.FileMode `json:"mode"`
	Modified int64       `json:"modified"`
}

func Describe(path string, info os.FileInfo) Metadata {
	return Metadata{Path: path, Bytes: info.Size(), ModeBits: info.Mode(), Modified: info.ModTime().UnixNano()}
}

func (m Metadata) Name() string       { return filepath.Base(m.Path) }
func (m Metadata) Size() int64        { return m.Bytes }
func (m Metadata) Mode() os.FileMode  { return m.ModeBits }
func (m Metadata) ModTime() time.Time { return time.Unix(0, m.Modified) }
func (m Metadata) IsDir() bool        { return m.ModeBits.IsDir() }
func (m Metadata) Sys() any           { return nil }

func (s Store) Stat(ctx context.Context, path string) ([]Metadata, error) {
	source, _, err := s.requestContext(ctx, "stat", path, "")
	if err != nil {
		return nil, err
	}
	defer source.Close()
	decoder := json.NewDecoder(source)
	var result []Metadata
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var entry Metadata
		if err := decoder.Decode(&entry); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		if _, err := Parts(entry.Path, true); err != nil ||
			entry.Path != filepath.Clean(path) && filepath.Dir(entry.Path) != filepath.Clean(path) {
			return nil, os.ErrPermission
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func accessDenied(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, unix.ENOENT) ||
		errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENXIO)
}

func describeArtifact(parent int, parts []string, path string) (Metadata, error) {
	fd, err := openArtifact(parent, parts)
	if err != nil {
		return Metadata{}, err
	}
	source := os.NewFile(uintptr(fd), path)
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return Metadata{}, err
	}
	return Describe(path, info), nil
}

func statArtifacts(images int, parts []string, path string) (int, error) {
	fd, err := openArtifact(images, parts)
	if err != nil {
		return -1, err
	}
	source := os.NewFile(uintptr(fd), path)
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return -1, err
	}
	outputFD, err := unix.MemfdCreate("sonic-image-metadata", unix.MFD_CLOEXEC)
	if err != nil {
		return -1, err
	}
	output := os.NewFile(uintptr(outputFD), "sonic-image-metadata")
	defer output.Close()
	encoder := json.NewEncoder(output)
	if !info.IsDir() {
		err = encoder.Encode(Describe(filepath.Clean(path), info))
	} else {
		for {
			entries, readErr := source.ReadDir(128)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return -1, readErr
			}
			for _, entry := range entries {
				reportPath := filepath.Join(path, entry.Name())
				if _, err := Parts(reportPath, true); err != nil {
					continue
				}
				metadata, err := describeArtifact(fd, []string{entry.Name()}, reportPath)
				if accessDenied(err) {
					continue
				}
				if err != nil {
					return -1, err
				}
				if err := encoder.Encode(metadata); err != nil {
					return -1, err
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
		}
	}
	if err != nil {
		return -1, err
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		return -1, err
	}
	return unix.FcntlInt(output.Fd(), unix.F_DUPFD_CLOEXEC, 0)
}
