package file

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
)

var openImageFile = func(ctx context.Context, root, path string) (*os.File, error) {
	return (imageexport.Store{HostRoot: root}).OpenContext(ctx, path)
}

var imageStats = func(ctx context.Context, path string) ([]imageexport.Metadata, error) {
	root, err := readPathRoot()
	if err != nil {
		return nil, err
	}
	return (imageexport.Store{HostRoot: root}).Stat(ctx, path)
}

func sensitiveReadPath(path string) bool {
	_, err := imageexport.Parts(path, true)
	return err != nil
}

func readPathRoot() (string, error) {
	_, err := os.Stat(hostRoot)
	if os.IsNotExist(err) {
		return "/", nil
	}
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(hostRoot)
}

func openReadPathContext(ctx context.Context, path string) (*os.File, error) {
	root, err := readPathRoot()
	if err != nil {
		return nil, err
	}
	logical := path
	if _, err := os.Stat(hostRoot); err == nil {
		relative, err := filepath.Rel(filepath.Clean(hostRoot), path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
			return nil, os.ErrPermission
		}
		logical = "/" + relative
	}
	if sensitiveReadPath(logical) {
		return nil, os.ErrPermission
	}
	return openImageFile(ctx, root, filepath.Clean(logical))
}
