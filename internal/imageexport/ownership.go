package imageexport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// This root-only journal and lock are shared with sonic-host-services.
type ownershipRecord struct {
	Version int    `json:"version"`
	UID     uint32 `json:"uid"`
	GID     uint32 `json:"gid"`
	NextUID uint32 `json:"next_uid"`
	NextGID uint32 `json:"next_gid"`
}

func (r ownershipRecord) valid() bool {
	return r.Version == 1 && r.UID > 0 && r.GID > 0 && r.UID < 1<<32-1 && r.GID < 1<<32-1 &&
		r.NextUID < 1<<32-1 && r.NextGID < 1<<32-1 && (r.NextUID == 0) == (r.NextGID == 0)
}

func rootRecord(base int, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(base, name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	if err := checkOwned(fd, 0, 0, 0600, false); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func readOwnership(base int) (ownershipRecord, error) {
	var record ownershipRecord
	file, err := rootRecord(base, "ownership.json", unix.O_RDONLY)
	if err != nil {
		return record, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 513))
	if err != nil {
		return record, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if len(data) > 512 || decoder.Decode(&record) != nil || !record.valid() {
		return record, fmt.Errorf("invalid managed-image ownership journal")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return record, fmt.Errorf("invalid managed-image ownership journal suffix")
	}
	return record, nil
}

func writeOwnership(base int, record ownershipRecord) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	name := ".ownership-" + hex.EncodeToString(random[:])
	file, err := rootRecord(base, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	defer file.Close()
	defer func() {
		if err := unix.Unlinkat(base, name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			fmt.Fprintln(os.Stderr, "Remove temporary ownership journal:", err)
		}
	}()
	if err := json.NewEncoder(file).Encode(record); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := unix.Renameat(base, name, base, "ownership.json"); err != nil {
		return err
	}
	return unix.Fsync(base)
}

func walkOwnership(ctx context.Context, parent int, name string, record ownershipRecord, migrate, staging bool, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fd, err := unix.Openat(parent, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() { file.Close() }()
	var info unix.Stat_t
	validate := func() error {
		if err := unix.Fstat(fd, &info); err != nil {
			return err
		}
		known := info.Uid == record.UID && info.Gid == record.GID ||
			record.NextUID != 0 && info.Uid == record.NextUID && info.Gid == record.NextGID
		directory := info.Mode&unix.S_IFMT == unix.S_IFDIR
		mode := uint32(0600)
		if directory {
			mode = 0700
		}
		if !known || info.Mode&07777 != mode ||
			(!directory && (info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1)) ||
			depth > 16 || depth == 0 && !directory || staging && depth != 0 && directory {
			return os.ErrPermission
		}
		return nil
	}
	if err := validate(); err != nil {
		return err
	}
	// Inspect type before opening: even opening a device as root is unsafe.
	flags := unix.O_RDONLY | unix.O_NONBLOCK | unix.O_CLOEXEC
	if info.Mode&unix.S_IFMT == unix.S_IFDIR {
		flags |= unix.O_DIRECTORY
	}
	opened, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", fd), flags, 0)
	if err != nil {
		return err
	}
	file.Close()
	fd = opened
	file = os.NewFile(uintptr(fd), name)
	if info.Mode&unix.S_IFMT == unix.S_IFDIR {
		for {
			names, err := file.Readdirnames(128)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			for _, child := range names {
				if !componentPattern.MatchString(child) || staging && !tokenPattern.MatchString(child) {
					return os.ErrPermission
				}
				if err := walkOwnership(ctx, fd, child, record, migrate, staging, depth+1); err != nil {
					return err
				}
			}
			if errors.Is(err, io.EOF) {
				break
			}
		}
	}
	if migrate {
		// Recheck the pinned inode, never chown a pathname or read its contents.
		if err := validate(); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := unix.Fchown(fd, int(record.NextUID), int(record.NextGID)); err != nil {
			return err
		}
		return file.Sync()
	}
	return nil
}

func ownershipTrees(ctx context.Context, base int, record ownershipRecord, migrate bool) error {
	for _, name := range []string{"images", "staging"} {
		if err := walkOwnership(ctx, base, name, record, migrate, name == "staging", 0); err != nil {
			return fmt.Errorf("validate managed-image ownership: %w", err)
		}
	}
	return nil
}

func prepareOwnership(ctx context.Context, base int, uid, gid uint32) error {
	lock, err := rootRecord(base, "ownership.lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return err
	}
	defer lock.Close()
	for {
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	record, err := readOwnership(base)
	missing := errors.Is(err, unix.ENOENT)
	if err != nil && !missing {
		return err
	}
	if missing {
		record = ownershipRecord{Version: 1, UID: uid, GID: gid}
	}
	for _, name := range []string{"images", "staging"} {
		// Only create missing roots. Existing roots may be partly migrated.
		if err := unix.Mkdirat(base, name, 0700); err == nil {
			if err := ensureNewOwnership(base, name, record.UID, record.GID); err != nil {
				return err
			}
		} else if !errors.Is(err, unix.EEXIST) {
			return err
		}
	}
	if missing {
		if err := ownershipTrees(ctx, base, record, false); err != nil {
			return err
		}
		if err := writeOwnership(base, record); err != nil {
			return err
		}
	}
	for record.NextUID != 0 || record.UID != uid || record.GID != gid {
		if record.NextUID == 0 {
			if err := ownershipTrees(ctx, base, record, false); err != nil {
				return err
			}
			record.NextUID, record.NextGID = uid, gid
			if err := writeOwnership(base, record); err != nil {
				return err
			}
		}
		if err := ownershipTrees(ctx, base, record, false); err != nil {
			return err
		}
		if err := ownershipTrees(ctx, base, record, true); err != nil {
			return err
		}
		record.UID, record.GID = record.NextUID, record.NextGID
		record.NextUID, record.NextGID = 0, 0
		if err := writeOwnership(base, record); err != nil {
			return err
		}
	}
	for _, name := range []string{"images", "staging"} {
		dir, err := openDirectory(name, base)
		if err != nil {
			return err
		}
		err = checkOwned(dir, int(uid), int(gid), 0700, true)
		unix.Close(dir)
		if err != nil {
			return err
		}
	}
	return nil
}

func ensureNewOwnership(base int, name string, uid, gid uint32) error {
	dir, err := openDirectory(name, base)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	if err := unix.Fchown(dir, int(uid), int(gid)); err != nil {
		return err
	}
	if err := unix.Fchmod(dir, 0700); err != nil {
		return err
	}
	return unix.Fsync(dir)
}
