package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var blockedReadDirectories = []string{
	"/root", "/proc", "/sys", "/dev", "/run",
	"/etc/sonic", "/etc/security", "/etc/ssl/private", "/etc/sudoers.d",
	"/host/glome",
}

var blockedReadFiles = []string{
	"/etc/shadow", "/etc/shadow-", "/etc/gshadow", "/etc/gshadow-", "/etc/sudoers",
}

var openReadFile = os.OpenFile

func sensitiveReadPath(path string) bool {
	path = filepath.Clean(path)
	// Apply the same policy to SONiC's host-overlay backing files.
	parts := strings.Split(path, "/")
	for len(parts) > 4 && parts[1] == "host" && strings.HasPrefix(parts[2], "image-") && parts[3] == "rw" {
		path = "/" + strings.Join(parts[4:], "/")
		parts = strings.Split(path, "/")
	}
	for _, blocked := range blockedReadFiles {
		if path == blocked || strings.HasPrefix(path, blocked+".") {
			return true
		}
	}
	for _, prefix := range blockedReadDirectories {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".ssh" || part == ".gnupg" {
			return true
		}
	}
	name := filepath.Base(path)
	for _, blocked := range []string{"config_db.json", "golden_config_db.json", "minigraph.xml"} {
		if name == blocked || strings.HasPrefix(name, blocked+".") {
			return true
		}
	}
	return strings.HasPrefix(name, "ssh_host_") && !strings.HasSuffix(name, ".pub")
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

func checkPhysicalReadPath(path, root string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return os.ErrPermission
	}
	if sensitiveReadPath("/" + relative) {
		return os.ErrPermission
	}
	return nil
}

func resolveReadPath(path, root string) (string, error) {
	for links := 0; links <= 40; links++ {
		if err := checkPhysicalReadPath(path, root); err != nil {
			return "", err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", err
		}
		parts := strings.Split(relative, string(filepath.Separator))
		current := root
		followed := false
		for index, part := range parts {
			current = filepath.Join(current, part)
			if err := checkPhysicalReadPath(current, root); err != nil {
				return "", err
			}
			info, err := os.Lstat(current)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Readlink(current)
			if err != nil {
				return "", err
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(current), target)
			}
			path = filepath.Join(append([]string{target}, parts[index+1:]...)...)
			followed = true
			break
		}
		if !followed {
			return path, nil
		}
	}
	return "", &os.PathError{Op: "readlink", Path: path, Err: syscall.ELOOP}
}

func openReadPath(path string, flags int) (*os.File, error) {
	root, err := readPathRoot()
	if err != nil {
		return nil, err
	}
	// Translation uses hostRoot's spelling, which may itself be a symlink.
	if _, err := os.Stat(hostRoot); err == nil && root != filepath.Clean(hostRoot) {
		relative, err := filepath.Rel(filepath.Clean(hostRoot), path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, "../") {
			path = filepath.Join(root, relative)
		}
	}
	resolved, err := resolveReadPath(path, root)
	if err != nil {
		return nil, err
	}
	source, err := openReadFile(resolved, flags|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, os.ErrPermission
		}
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			source.Close()
		}
	}()
	// Validate the opened object to close the path-check/open race.
	actual, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", source.Fd()))
	if err != nil {
		return nil, err
	}
	if err := checkPhysicalReadPath(actual, root); err != nil {
		return nil, err
	}
	info, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(actual, " (deleted)") || info.Mode()&os.ModeSymlink != 0 {
		return nil, os.ErrPermission
	}
	// A hard-linked file has no unique canonical name for the block-list.
	if st, ok := info.Sys().(*syscall.Stat_t); ok && info.Mode().IsRegular() && st.Nlink != 1 {
		return nil, os.ErrPermission
	}
	keep = true
	return source, nil
}

func statReadPath(path string) (os.FileInfo, error) {
	// O_PATH preserves Stat support for sockets and other non-readable objects.
	source, err := openReadPath(path, unix.O_PATH)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	return source.Stat()
}

func readDirPath(path string) ([]os.DirEntry, error) {
	source, err := openReadPath(path, os.O_RDONLY)
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
