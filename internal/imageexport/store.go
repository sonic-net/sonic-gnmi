// Package imageexport restricts file access to privately staged image artifacts.
package imageexport

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	ServiceUser = "sonic-image"
	StoreRoot   = "/host/sonic-image-exports"
	ExportRoot  = StoreRoot + "/images"
	workerPath  = "/usr/sbin/sonic-image-worker"
)

var (
	componentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	tokenPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	newWorkerCommand = func(ctx context.Context, args ...string) *exec.Cmd {
		return exec.CommandContext(ctx, workerPath, args...)
	}
)

func Parts(path string, directory bool) ([]string, error) {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return nil, os.ErrPermission
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return nil, os.ErrPermission
		}
	}
	path = filepath.Clean(path)
	if directory && path == ExportRoot {
		return nil, nil
	}
	if !strings.HasPrefix(path, ExportRoot+"/") {
		return nil, os.ErrPermission
	}
	parts := strings.Split(strings.TrimPrefix(path, ExportRoot+"/"), "/")
	if len(parts) > 16 {
		return nil, os.ErrPermission
	}
	for _, part := range parts {
		if !componentPattern.MatchString(part) {
			return nil, os.ErrPermission
		}
	}
	return parts, nil
}

type Store struct {
	HostRoot string
}

func (s Store) physical(path string) string {
	return filepath.Join(s.HostRoot, strings.TrimPrefix(path, "/"))
}

func (s Store) credentials() (*syscall.Credential, error) {
	data, err := os.ReadFile(s.physical("/etc/passwd"))
	if err != nil {
		return nil, fmt.Errorf("managed-image account lookup failed: %w", err)
	}
	var result *syscall.Credential
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if fields[0] != ServiceUser {
			continue
		}
		if len(fields) != 7 || result != nil || fields[6] != "/usr/sbin/nologin" {
			return nil, fmt.Errorf("invalid managed-image service account")
		}
		uid, uidErr := strconv.ParseUint(fields[2], 10, 32)
		gid, gidErr := strconv.ParseUint(fields[3], 10, 32)
		if uidErr != nil || gidErr != nil || uid == 0 || gid == 0 || uid == 1<<32-1 || gid == 1<<32-1 {
			return nil, fmt.Errorf("invalid managed-image service identity")
		}
		result = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}
	}
	if result == nil {
		return nil, fmt.Errorf("managed-image service account is unavailable")
	}
	return result, nil
}

func (s Store) request(operation, path, token string) (*os.File, string, error) {
	return s.requestContext(context.Background(), operation, path, token)
}

func (s Store) requestContext(parent context.Context, operation, path, token string) (source *os.File, resultToken string, resultErr error) {
	if err := parent.Err(); err != nil {
		return nil, "", err
	}
	if _, err := Parts(path, operation == "open" || operation == "stat"); err != nil {
		return nil, "", err
	}
	credential, err := s.credentials()
	if err != nil {
		return nil, "", err
	}
	if operation != "abort" {
		if err := s.PrepareContext(parent); err != nil {
			return nil, "", err
		}
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_SEQPACKET|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	defer unix.Close(pair[0])
	child := os.NewFile(uintptr(pair[1]), "image-worker-socket")
	defer child.Close()
	if err := unix.SetsockoptTimeval(pair[0], unix.SOL_SOCKET, unix.SO_RCVTIMEO,
		&unix.Timeval{Sec: 30}); err != nil {
		return nil, "", err
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd := newWorkerCommand(ctx, s.HostRoot, operation, path, token)
	cmd.ExtraFiles = []*os.File{child}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start managed-image worker: %w", err)
	}
	if operation == "create" {
		defer func() {
			if resultErr != nil {
				upload := Upload{store: s, path: path, token: token, closed: true}
				if err := upload.Abort(); err != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("reclaim failed managed-image creation: %w", err))
				}
			}
		}()
	}
	child.Close()
	payload, ancillary := make([]byte, 128), make([]byte, unix.CmsgSpace(4))
	n, ancillaryN, flags, _, receiveErr := unix.Recvmsg(
		pair[0], payload, ancillary, unix.MSG_CMSG_CLOEXEC)
	if receiveErr != nil {
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	var descriptors []int
	defer func() {
		for _, descriptor := range descriptors {
			unix.Close(descriptor)
		}
	}()
	messages, err := unix.ParseSocketControlMessage(ancillary[:ancillaryN])
	if err != nil {
		return nil, "", err
	}
	for _, message := range messages {
		if message.Header.Level == unix.SOL_SOCKET && message.Header.Type == unix.SCM_RIGHTS {
			rights, err := unix.ParseUnixRights(&message)
			if err != nil {
				return nil, "", err
			}
			descriptors = append(descriptors, rights...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if receiveErr != nil {
		return nil, "", fmt.Errorf("receive managed-image descriptor: %w", receiveErr)
	}
	response := string(payload[:n])
	if response == "DENIED" {
		return nil, "", os.ErrPermission
	}
	expectedFDs := 0
	if operation == "open" || operation == "create" || operation == "stat" {
		expectedFDs = 1
	}
	if waitErr != nil || flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) != 0 ||
		!strings.HasPrefix(response, "OK") || len(descriptors) != expectedFDs {
		return nil, "", fmt.Errorf("managed-image worker failed: %s", stderr.String())
	}
	resultToken = strings.TrimSpace(strings.TrimPrefix(response, "OK"))
	if operation == "create" && (!tokenPattern.MatchString(resultToken) || resultToken != token) {
		return nil, "", fmt.Errorf("invalid managed-image staging response")
	}
	if expectedFDs == 1 {
		source = os.NewFile(uintptr(descriptors[0]), path)
		descriptors = nil
	}
	return source, resultToken, nil
}

func (s Store) Open(path string) (*os.File, error) {
	return s.OpenContext(context.Background(), path)
}

func (s Store) OpenContext(ctx context.Context, path string) (*os.File, error) {
	source, _, err := s.requestContext(ctx, "open", path, "")
	return source, err
}

func (s Store) Remove(ctx context.Context, path string) error {
	_, _, err := s.requestContext(ctx, "remove", path, "")
	return err
}

func (s Store) Prepare() error {
	return s.PrepareContext(context.Background())
}

func (s Store) PrepareContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	credential, err := s.credentials()
	if err != nil {
		return err
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("image-store provisioning requires root")
	}
	host, err := openDirectory(s.physical("/host"), unix.AT_FDCWD)
	if err != nil {
		return err
	}
	defer unix.Close(host)
	var info unix.Stat_t
	if err := unix.Fstat(host, &info); err != nil {
		return err
	}
	if info.Uid != 0 || info.Mode&0022 != 0 {
		return os.ErrPermission
	}
	if err := ensureDirectory(host, "sonic-image-exports", 0, 0, 0755); err != nil {
		return err
	}
	base, err := openDirectory("sonic-image-exports", host)
	if err != nil {
		return err
	}
	defer unix.Close(base)
	return prepareOwnership(ctx, base, credential.Uid, credential.Gid)
}

func ensureDirectory(parent int, name string, uid, gid int, mode uint32) error {
	err := unix.Mkdirat(parent, name, mode)
	created := err == nil
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return err
	}
	dir, err := openDirectory(name, parent)
	if err != nil {
		return err
	}
	defer unix.Close(dir)
	if created {
		if err := unix.Fchown(dir, uid, gid); err != nil {
			return err
		}
		if err := unix.Fchmod(dir, mode); err != nil {
			return err
		}
	}
	return checkOwned(dir, uid, gid, mode, true)
}

type Upload struct {
	File      *os.File
	store     Store
	path      string
	token     string
	closed    bool
	committed bool
}

func (s Store) Create(path string) (*Upload, error) {
	return s.CreateContext(context.Background(), path)
}

func (s Store) CreateContext(ctx context.Context, path string) (*Upload, error) {
	if _, err := Parts(path, false); err != nil {
		return nil, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(random[:])
	file, _, err := s.requestContext(ctx, "create", path, token)
	if err != nil {
		return nil, err
	}
	return &Upload{File: file, store: s, path: path, token: token}, nil
}

func (u *Upload) Commit() error {
	return u.CommitContext(context.Background())
}

func (u *Upload) CommitContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := u.File.Sync(); err != nil {
		return err
	}
	closeErr := u.File.Close()
	u.closed = true
	if closeErr != nil {
		return closeErr
	}
	_, _, err := u.store.requestContext(ctx, "publish", u.path, u.token)
	u.committed = err == nil
	return err
}

func (u *Upload) Abort() error {
	if u.committed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var closeErr error
	if !u.closed {
		closeErr = u.File.Close()
		u.closed = true
	}
	_, _, err := u.store.requestContext(ctx, "abort", u.path, u.token)
	return errors.Join(closeErr, err)
}
