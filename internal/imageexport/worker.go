package imageexport

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func openDirectory(path string, parent int) (int, error) {
	return unix.Openat(parent, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
}

func checkOwned(fd, uid, gid int, mode uint32, directory bool) error {
	var info unix.Stat_t
	if err := unix.Fstat(fd, &info); err != nil {
		return err
	}
	kind := uint32(unix.S_IFREG)
	if directory {
		kind = unix.S_IFDIR
	}
	if info.Mode&unix.S_IFMT != kind || info.Uid != uint32(uid) || info.Gid != uint32(gid) ||
		info.Mode&07777 != mode || (!directory && info.Nlink != 1) {
		return os.ErrPermission
	}
	return nil
}

func artifactParent(images int, parts []string, create bool) (int, error) {
	current, err := unix.Dup(images)
	if err != nil {
		return -1, err
	}
	defer func() {
		if current >= 0 {
			unix.Close(current)
		}
	}()
	for _, part := range parts {
		if create {
			if err := unix.Mkdirat(current, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
				return -1, err
			}
		}
		next, err := openDirectory(part, current)
		if err != nil {
			return -1, err
		}
		if err := checkOwned(next, os.Geteuid(), os.Getegid(), 0700, true); err != nil {
			unix.Close(next)
			return -1, err
		}
		unix.Close(current)
		current = next
	}
	result := current
	current = -1
	return result, nil
}

func openArtifact(images int, parts []string) (int, error) {
	if len(parts) == 0 {
		return unix.Dup(images)
	}
	parent, err := artifactParent(images, parts[:len(parts)-1], false)
	if err != nil {
		return -1, err
	}
	defer unix.Close(parent)
	source, err := unix.Openat(parent, parts[len(parts)-1],
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	var info unix.Stat_t
	if err := unix.Fstat(source, &info); err != nil {
		unix.Close(source)
		return -1, err
	}
	directory := info.Mode&unix.S_IFMT == unix.S_IFDIR
	mode := uint32(0600)
	if directory {
		mode = 0700
	}
	if err := checkOwned(source, os.Geteuid(), os.Getegid(), mode, directory); err != nil {
		unix.Close(source)
		return -1, err
	}
	return source, nil
}

func workerOperation(root, operation, path, token string) (int, string, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return -1, "", err
	}
	if os.Geteuid() == 0 || os.Getegid() == 0 || os.Getuid() != os.Geteuid() || len(groups) != 0 {
		return -1, "", os.ErrPermission
	}
	parts, err := Parts(path, operation == "open")
	if err != nil {
		return -1, "", err
	}
	store := Store{HostRoot: root}
	images, err := openDirectory(store.physical(ExportRoot), unix.AT_FDCWD)
	if err != nil {
		return -1, "", err
	}
	defer unix.Close(images)
	if err := checkOwned(images, os.Geteuid(), os.Getegid(), 0700, true); err != nil {
		return -1, "", err
	}
	if operation == "open" {
		fd, err := openArtifact(images, parts)
		return fd, "", err
	}
	if operation != "create" && operation != "publish" && operation != "abort" {
		return -1, "", os.ErrPermission
	}
	staging, err := openDirectory(store.physical(StoreRoot+"/staging"), unix.AT_FDCWD)
	if err != nil {
		return -1, "", err
	}
	defer unix.Close(staging)
	if err := checkOwned(staging, os.Geteuid(), os.Getegid(), 0700, true); err != nil {
		return -1, "", err
	}
	if operation == "create" {
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			return -1, "", err
		}
		token = hex.EncodeToString(random[:])
		fd, err := unix.Openat(staging, token,
			unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
		if err != nil {
			return -1, "", err
		}
		if err := unix.Fchmod(fd, 0600); err != nil {
			unix.Close(fd)
			unix.Unlinkat(staging, token, 0)
			return -1, "", err
		}
		return fd, token, nil
	}
	if !tokenPattern.MatchString(token) {
		return -1, "", os.ErrPermission
	}
	if operation == "abort" {
		err := unix.Unlinkat(staging, token, 0)
		if errors.Is(err, unix.ENOENT) {
			err = nil
		}
		return -1, "", err
	}
	source, err := unix.Openat(staging, token, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", err
	}
	err = checkOwned(source, os.Geteuid(), os.Getegid(), 0600, false)
	unix.Close(source)
	if err != nil {
		return -1, "", err
	}
	destination, err := artifactParent(images, parts[:len(parts)-1], true)
	if err != nil {
		return -1, "", err
	}
	defer unix.Close(destination)
	return -1, "", unix.Renameat(staging, token, destination, parts[len(parts)-1])
}

// ServeWorker must execute in a child that already has unprivileged credentials.
func ServeWorker(socket int, root, operation, path, token string) int {
	fd, resultToken, err := workerOperation(root, operation, path, token)
	if fd >= 0 {
		defer unix.Close(fd)
	}
	if err != nil {
		denied := errors.Is(err, os.ErrPermission) || errors.Is(err, unix.ENOENT) ||
			errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENXIO)
		response := "DENIED"
		if !denied {
			response = "ERROR"
			fmt.Fprintln(os.Stderr, "Managed-image operation failed:", err)
		}
		if sendErr := unix.Sendmsg(socket, []byte(response), nil, nil, 0); sendErr != nil {
			fmt.Fprintln(os.Stderr, "Managed-image response failed:", sendErr)
		}
		return 1
	}
	var rights []byte
	if fd >= 0 {
		rights = unix.UnixRights(fd)
	}
	if err := unix.Sendmsg(socket, []byte("OK "+resultToken), rights, nil, 0); err != nil {
		fmt.Fprintln(os.Stderr, "Managed-image descriptor transfer failed:", err)
		return 1
	}
	return 0
}
