package gnmi

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// newHostKey generates an ed25519 SSH host key signer.
func newHostKey(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

// writePubKey writes key in authorized_keys format (as sshd's *.pub files are)
// to a temp file and returns its path.
func writePubKey(t *testing.T, key ssh.PublicKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh_host_key.pub")
	if err := os.WriteFile(path, ssh.MarshalAuthorizedKey(key), 0644); err != nil {
		t.Fatalf("write pub key: %v", err)
	}
	return path
}

// startSSHServer starts an in-process SSH server presenting hostKey that
// accepts only user/password. It returns the listen address.
func startSSHServer(t *testing.T, hostKey ssh.Signer, user, password string) string {
	t.Helper()
	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == user && string(pass) == password {
				return nil, nil
			}
			return nil, errors.New("bad credentials")
		},
	}
	config.AddHostKey(hostKey)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, chans, reqs, err := ssh.NewServerConn(conn, config)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for ch := range chans {
					ch.Reject(ssh.Prohibited, "no channels")
				}
			}(conn)
		}
	}()
	return ln.Addr().String()
}

// useSSHAuthTarget points UserPwAuth at addr with keyPaths as the pinned host
// keys, restoring the defaults when the test ends.
func useSSHAuthTarget(t *testing.T, addr string, keyPaths []string) {
	t.Helper()
	oldAddr, oldPaths := sshAuthAddr, sshHostKeyPaths
	sshAuthAddr, sshHostKeyPaths = addr, keyPaths
	t.Cleanup(func() { sshAuthAddr, sshHostKeyPaths = oldAddr, oldPaths })
}

func TestSshdHostKeyCallback(t *testing.T) {
	pinned := newHostKey(t).PublicKey()
	other := newHostKey(t).PublicKey()
	pinnedPath := writePubKey(t, pinned)

	dir := t.TempDir()
	missingPath := filepath.Join(dir, "missing.pub")
	garbagePath := filepath.Join(dir, "garbage.pub")
	if err := os.WriteFile(garbagePath, []byte("not a key\n"), 0644); err != nil {
		t.Fatal(err)
	}
	trailingPath := filepath.Join(dir, "trailing.pub")
	trailing := append(ssh.MarshalAuthorizedKey(pinned), ssh.MarshalAuthorizedKey(other)...)
	if err := os.WriteFile(trailingPath, trailing, 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("no usable keys fails closed", func(t *testing.T) {
		cb, err := sshdHostKeyCallback([]string{missingPath, garbagePath, trailingPath})
		if err == nil || cb != nil {
			t.Fatalf("expected error and nil callback, got cb=%v err=%v", cb != nil, err)
		}
		for _, p := range []string{missingPath, garbagePath + ": parse", trailingPath + ": unexpected trailing data"} {
			if !strings.Contains(err.Error(), p) {
				t.Errorf("error %q does not mention %q", err, p)
			}
		}
	})

	t.Run("unusable entries are skipped", func(t *testing.T) {
		cb, err := sshdHostKeyCallback([]string{missingPath, garbagePath, pinnedPath})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := cb("127.0.0.1:22", nil, pinned); err != nil {
			t.Errorf("pinned key rejected: %v", err)
		}
		if err := cb("127.0.0.1:22", nil, other); err == nil {
			t.Error("unpinned key accepted")
		}
	})
}

func TestUserPwAuth(t *testing.T) {
	const user, password = "admin", "s3cret"
	hostKey := newHostKey(t)
	addr := startSSHServer(t, hostKey, user, password)
	pinnedPath := writePubKey(t, hostKey.PublicKey())

	t.Run("valid credentials", func(t *testing.T) {
		useSSHAuthTarget(t, addr, []string{pinnedPath})
		ok, err := UserPwAuth(user, password)
		if !ok || err != nil {
			t.Fatalf("UserPwAuth = %v, %v; want true, nil", ok, err)
		}
	})

	t.Run("wrong password", func(t *testing.T) {
		useSSHAuthTarget(t, addr, []string{pinnedPath})
		ok, err := UserPwAuth(user, "wrong")
		if ok || err == nil {
			t.Fatalf("UserPwAuth = %v, %v; want false, error", ok, err)
		}
	})

	t.Run("host key mismatch", func(t *testing.T) {
		useSSHAuthTarget(t, addr, []string{writePubKey(t, newHostKey(t).PublicKey())})
		ok, err := UserPwAuth(user, password)
		if ok || err == nil || !strings.Contains(err.Error(), "host key mismatch") {
			t.Fatalf("UserPwAuth = %v, %v; want false, host key mismatch", ok, err)
		}
	})

	t.Run("no host keys", func(t *testing.T) {
		useSSHAuthTarget(t, addr, []string{filepath.Join(t.TempDir(), "missing.pub")})
		ok, err := UserPwAuth(user, password)
		if ok || err == nil || !strings.Contains(err.Error(), "no usable sshd host keys") {
			t.Fatalf("UserPwAuth = %v, %v; want false, no usable sshd host keys", ok, err)
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		closedAddr := ln.Addr().String()
		ln.Close()
		useSSHAuthTarget(t, closedAddr, []string{pinnedPath})
		ok, err := UserPwAuth(user, password)
		if ok || err == nil {
			t.Fatalf("UserPwAuth = %v, %v; want false, error", ok, err)
		}
	})

	t.Run("stalled handshake times out", func(t *testing.T) {
		// Accept the TCP connection but never speak SSH.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		defer ln.Close()
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			io.Copy(io.Discard, conn) // returns once the client gives up

		}()

		useSSHAuthTarget(t, ln.Addr().String(), []string{pinnedPath})
		oldTimeout := sshAuthTimeout
		sshAuthTimeout = 200 * time.Millisecond
		defer func() { sshAuthTimeout = oldTimeout }()

		start := time.Now()
		ok, err := UserPwAuth(user, password)
		if ok || err == nil {
			t.Fatalf("UserPwAuth = %v, %v; want false, error", ok, err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("UserPwAuth took %v; handshake deadline not enforced", elapsed)
		}
	})
}
