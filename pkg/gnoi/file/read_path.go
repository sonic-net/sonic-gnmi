package file

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var errReadFileType = errors.New("not a regular file or directory")

// Check the target inode and pin each publicly traversable ancestor, rather
// than checking permissions on a pathname and then reopening a different file.
func openPublicReadFile(path string) (*os.File, error) {
	root := hostRoot
	if _, err := os.Stat(root); os.IsNotExist(err) {
		root = "/"
	} else if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return nil, os.ErrPermission
	}
	current, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		if current != nil {
			current.Close()
		}
	}()
	for _, part := range strings.Split(relative, "/") {
		if part == "." {
			continue
		}
		if err := checkPublicReadMode(current, 0001); err != nil {
			return nil, err
		}
		fd, err := unix.Openat(int(current.Fd()), part, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		pinned := os.NewFile(uintptr(fd), part)
		info, err := pinned.Stat()
		if err != nil {
			pinned.Close()
			return nil, err
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			pinned.Close()
			return nil, errReadFileType
		}
		next, err := os.OpenFile("/proc/self/fd/"+strconv.Itoa(fd), os.O_RDONLY|unix.O_NONBLOCK, 0)
		pinned.Close()
		if err != nil {
			return nil, err
		}
		current.Close()
		current = next
	}
	info, err := current.Stat()
	if err != nil {
		return nil, err
	}
	required := os.FileMode(0004)
	if info.IsDir() {
		required |= 0001
	}
	if err := checkPublicReadMode(current, required); err != nil {
		return nil, err
	}
	result := current
	current = nil
	return result, nil
}

func checkPublicReadMode(file *os.File, required os.FileMode) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm()&required != required {
		return os.ErrPermission
	}
	// Mode bits cannot describe named-user/group ACL restrictions. Reject
	// extended access ACLs instead of treating the "other" bits as sufficient.
	_, err = unix.Fgetxattr(int(file.Fd()), "system.posix_acl_access", nil)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	if err == nil {
		return os.ErrPermission
	}
	return err
}
