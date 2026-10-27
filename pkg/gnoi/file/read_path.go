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

func openPublicReadRoot() (*os.File, error) {
	root := hostRoot
	if _, err := os.Stat(root); os.IsNotExist(err) {
		root = "/"
	} else if err != nil {
		return nil, err
	}
	fd, err := unix.Open(root, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), root), nil
}

func openPublicReadFile(path string) (*os.File, error) {
	root, err := openPublicReadRoot()
	if err != nil {
		return nil, err
	}
	return walkPublicReadFile([]*os.File{root}, path)
}

func openPublicReadChild(parent *os.File, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
		return nil, os.ErrPermission
	}
	root, err := openPublicReadRoot()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	rootInfo, err := root.Stat()
	if err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), ".", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	directories := []*os.File{os.NewFile(uintptr(fd), parent.Name())}
	defer func() {
		for _, directory := range directories {
			directory.Close()
		}
	}()
	// Recover a pinned root-to-parent stack without reopening the request path.
	// Relative symlinks can then traverse .. while absolute links use the host root.
	for {
		current := directories[len(directories)-1]
		if err := checkPublicReadMode(current, 0001); err != nil {
			return nil, err
		}
		info, err := current.Stat()
		if err != nil {
			return nil, err
		}
		if os.SameFile(info, rootInfo) {
			break
		}
		fd, err := unix.Openat(int(current.Fd()), "..", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		ancestor := os.NewFile(uintptr(fd), "..")
		directories = append(directories, ancestor)
		ancestorInfo, err := ancestor.Stat()
		if err != nil {
			return nil, err
		}
		if os.SameFile(info, ancestorInfo) {
			return nil, os.ErrPermission
		}
	}
	for i, j := 0, len(directories)-1; i < j; i, j = i+1, j-1 {
		directories[i], directories[j] = directories[j], directories[i]
	}
	stack := directories
	directories = nil
	return walkPublicReadFile(stack, name)
}

// Walk the original path so resolving a symlink cannot skip a private ancestor.
func walkPublicReadFile(directories []*os.File, path string) (_ *os.File, err error) {
	defer func() {
		if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			err = os.ErrPermission
		}
	}()
	defer func() {
		for _, directory := range directories {
			directory.Close()
		}
	}()
	pending := strings.Split(path, "/")
	links := 0
	for len(pending) != 0 {
		current := directories[len(directories)-1]
		if err := checkPublicReadMode(current, 0001); err != nil {
			return nil, err
		}
		part := pending[0]
		pending = pending[1:]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			if len(directories) == 1 {
				return nil, os.ErrPermission
			}
			current.Close()
			directories = directories[:len(directories)-1]
			continue
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
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			buffer := make([]byte, 4096)
			n, err := unix.Readlinkat(fd, "", buffer)
			pinned.Close()
			if err != nil {
				return nil, err
			}
			if links > 40 || n == len(buffer) {
				return nil, os.ErrPermission
			}
			target := string(buffer[:n])
			if filepath.IsAbs(target) {
				for _, directory := range directories[1:] {
					directory.Close()
				}
				directories = directories[:1]
			}
			pending = append(strings.Split(target, "/"), pending...)
			continue
		}
		if info.IsDir() {
			directories = append(directories, pinned)
			continue
		}
		defer pinned.Close()
		if len(pending) != 0 {
			return nil, os.ErrPermission
		}
		return openPublicReadTarget(pinned)
	}
	return openPublicReadTarget(directories[len(directories)-1])
}

func openPublicReadTarget(pinned *os.File) (*os.File, error) {
	info, err := pinned.Stat()
	if err != nil {
		return nil, err
	}
	required := os.FileMode(0004)
	if info.IsDir() {
		required |= 0001
	}
	if err := checkPublicReadMode(pinned, required); err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, errReadFileType
	}
	return os.OpenFile("/proc/self/fd/"+strconv.Itoa(int(pinned.Fd())), os.O_RDONLY|unix.O_NONBLOCK, 0)
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
	_, err = unix.Getxattr("/proc/self/fd/"+strconv.Itoa(int(file.Fd())), "system.posix_acl_access", nil)
	if errors.Is(err, unix.ENODATA) || errors.Is(err, unix.ENOTSUP) {
		return nil
	}
	if err == nil {
		return os.ErrPermission
	}
	return err
}
