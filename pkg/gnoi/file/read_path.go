package file

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
)

var openImageFile = func(root, path string) (*os.File, error) {
	return (imageexport.Store{HostRoot: root}).Open(path)
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

func openReadPath(path string) (*os.File, error) {
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
	return openImageFile(root, filepath.Clean(logical))
}

func statReadPath(path string) (os.FileInfo, error) {
	source, err := openReadPath(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return source.Stat()
}

func readDirPath(path string) ([]os.DirEntry, error) {
	source, err := openReadPath(path)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	entries, err := source.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}
