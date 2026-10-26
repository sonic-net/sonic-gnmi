package file

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
	"golang.org/x/sys/unix"
)

type legacyDirectory struct {
	file   *os.File
	name   string
	guards []unix.Stat_t
}

func (d *legacyDirectory) private(info unix.Stat_t) bool {
	for _, guard := range d.guards {
		if info.Dev == guard.Dev && info.Ino == guard.Ino || guard.Uid != 0 && info.Uid == guard.Uid {
			return true
		}
	}
	return false
}

func (d *legacyDirectory) checkFinal() (*unix.Stat_t, error) {
	var info unix.Stat_t
	err := unix.Fstatat(int(d.file.Fd()), d.name, &info, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if d.private(info) || info.Mode&unix.S_IFMT == unix.S_IFLNK ||
		info.Mode&unix.S_IFMT == unix.S_IFREG && info.Nlink != 1 {
		return nil, os.ErrPermission
	}
	return &info, nil
}

func pinLegacyDirectory(path string, create bool) (*legacyDirectory, error) {
	if managed, err := managedImageDestination(path); err != nil || managed {
		return nil, os.ErrPermission
	}
	root, err := readPathRoot()
	if err != nil {
		return nil, err
	}
	d := &legacyDirectory{name: filepath.Base(path)}
	for _, name := range []string{imageexport.StoreRoot, imageexport.ExportRoot, imageexport.StoreRoot + "/staging"} {
		fd, err := unix.Open(filepath.Join(root, name), unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var info unix.Stat_t
		err = unix.Fstat(fd, &info)
		unix.Close(fd)
		if err != nil {
			return nil, err
		}
		if info.Mode&unix.S_IFMT != unix.S_IFDIR {
			return nil, os.ErrPermission
		}
		d.guards = append(d.guards, info)
	}
	current, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() {
		if current >= 0 {
			unix.Close(current)
		}
	}()
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(filepath.Clean(path)), "/"), "/") {
		if part == "" {
			continue
		}
		if create {
			if err := unix.Mkdirat(current, part, 0755); err != nil && !errors.Is(err, unix.EEXIST) {
				return nil, err
			}
		}
		next, err := unix.Openat(current, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		var info unix.Stat_t
		err = unix.Fstat(next, &info)
		if err != nil || d.private(info) {
			unix.Close(next)
			if err != nil {
				return nil, err
			}
			return nil, os.ErrPermission
		}
		unix.Close(current)
		current = next
	}
	d.file = os.NewFile(uintptr(current), "legacy-upload-directory")
	current = -1
	if _, err := d.checkFinal(); err != nil {
		d.file.Close()
		return nil, err
	}
	return d, nil
}

type legacyUpload struct {
	File        *os.File
	directory   *legacyDirectory
	temp        string
	permissions os.FileMode
	owner       *unix.Stat_t
	closed      bool
	published   bool
}

func createLegacyUpload(path string) (*legacyUpload, error) {
	directory, err := pinLegacyDirectory(path, true)
	if err != nil {
		return nil, err
	}
	owner, err := directory.checkFinal()
	if err != nil {
		directory.file.Close()
		return nil, err
	}
	// The proc path refers to our pinned directory, never a caller's symlink.
	file, err := os.CreateTemp(fmt.Sprintf("/proc/self/fd/%d", directory.file.Fd()), ".gnoi-upload-*")
	if err != nil {
		directory.file.Close()
		return nil, err
	}
	permissions := os.FileMode(0644)
	if owner != nil {
		permissions = os.FileMode(owner.Mode & 0777)
	}
	return &legacyUpload{File: file, directory: directory, temp: filepath.Base(file.Name()),
		permissions: permissions, owner: owner}, nil
}

func (u *legacyUpload) Commit(permissions os.FileMode) error {
	if u.owner != nil {
		if err := u.File.Chown(int(u.owner.Uid), int(u.owner.Gid)); err != nil {
			return err
		}
	}
	if err := u.File.Chmod(permissions); err != nil {
		return err
	}
	if err := u.File.Sync(); err != nil {
		return err
	}
	err := u.File.Close()
	u.closed = true
	if err != nil {
		return err
	}
	if _, err := u.directory.checkFinal(); err != nil {
		return err
	}
	fd := int(u.directory.file.Fd())
	if err := unix.Renameat(fd, u.temp, fd, u.directory.name); err != nil {
		return err
	}
	u.published = true
	return u.directory.file.Sync()
}

func (u *legacyUpload) Abort() {
	if !u.closed {
		if err := u.File.Close(); err != nil {
			log.Errorf("Close legacy upload: %v", err)
		}
		u.closed = true
	}
	if !u.published {
		err := unix.Unlinkat(int(u.directory.file.Fd()), u.temp, 0)
		if err != nil && !errors.Is(err, unix.ENOENT) {
			log.Errorf("Remove incomplete legacy upload: %v", err)
		}
	}
	if err := u.directory.file.Close(); err != nil {
		log.Errorf("Close legacy upload directory: %v", err)
	}
}
