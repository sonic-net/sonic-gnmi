package gnmi

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/golang/glog"
	"github.com/msteinert/pam"
	"github.com/sonic-net/sonic-gnmi/common_utils"
	"golang.org/x/crypto/ssh"
	"net"
	"os"
	"os/user"
	"strings"
	"time"
)

// sshAuthAddr is the address of the local sshd used to validate credentials.
// The gnmi container runs with host networking, so this reaches the host sshd.
const sshAuthAddr = "127.0.0.1:22"

// sshAuthTimeout bounds both the TCP connect and the SSH handshake/auth phase
// so a rogue listener cannot hang the auth path indefinitely.
const sshAuthTimeout = 5 * time.Second

// sshHostKeyPaths lists candidate public host key files for the local sshd, in
// preference order. The gnmi container mounts the host root read-only at
// /mnt/host and uses host networking, so 127.0.0.1:22 reaches the host's sshd
// whose host keys live under /mnt/host/etc/ssh (the container's own /etc/ssh
// does not correspond to that sshd). These files are root-owned and not
// writable by unprivileged users, so they can be trusted as the identity of
// the real sshd. It is a var (not a const) so tests can point it at fixtures.
// SONiC's host-ssh-keygen.sh generates an RSA key by default; ecdsa/ed25519
// are also accepted in case sshd is configured with them.
var sshHostKeyPaths = []string{
	"/mnt/host/etc/ssh/ssh_host_ed25519_key.pub",
	"/mnt/host/etc/ssh/ssh_host_ecdsa_key.pub",
	"/mnt/host/etc/ssh/ssh_host_rsa_key.pub",
}

type UserCredential struct {
	Username string
	Password string
}

// PAM conversation handler.
func (u UserCredential) PAMConvHandler(s pam.Style, msg string) (string, error) {

	switch s {
	case pam.PromptEchoOff:
		return u.Password, nil
	case pam.PromptEchoOn:
		return u.Password, nil
	case pam.ErrorMsg:
		return "", nil
	case pam.TextInfo:
		return "", nil
	default:
		return "", errors.New("unrecognized conversation message style")
	}
}

// PAMAuthenticate performs PAM authentication for the user credentials provided
func (u UserCredential) PAMAuthenticate() error {
	tx, err := pam.StartFunc("login", u.Username, u.PAMConvHandler)
	if err != nil {
		return err
	}
	return tx.Authenticate(0)
}

func PAMAuthUser(u string, p string) error {

	cred := UserCredential{u, p}
	err := cred.PAMAuthenticate()
	return err
}
func GetUserRoles(usr *user.User) ([]string, error) {
	// Lookup Roles
	gids, err := usr.GroupIds()
	if err != nil {
		return nil, err
	}
	roles := make([]string, len(gids))
	for idx, gid := range gids {
		group, err := user.LookupGroupId(gid)
		if err != nil {
			return nil, err
		}
		roles[idx] = group.Name
	}
	return roles, nil
}
func PopulateAuthStruct(username string, auth *common_utils.AuthInfo, r []string) error {
	if len(r) == 0 {
		AuthLock.Lock()
		defer AuthLock.Unlock()
		usr, err := user.Lookup(username)
		if err != nil {
			return err
		}

		roles, err := GetUserRoles(usr)
		if err != nil {
			return err
		}
		auth.Roles = roles
	} else {
		auth.Roles = r
	}
	auth.User = username

	return nil
}

// sshdHostKeyCallback returns an ssh.HostKeyCallback pinned to the local
// sshd's public host key(s) read from paths. Every readable/parseable file is
// pinned, and a connection is accepted only if the presented key exactly
// matches one of them (comparison is over the marshaled key, which includes
// the key type, so a different type can never be mistaken for a match).
//
// It fails closed: if none of the candidate files can be read/parsed, an error
// is returned so authentication is denied rather than silently falling back to
// accepting any host key (as ssh.InsecureIgnoreHostKey would). Pinning ensures
// we are actually talking to the real sshd and not a rogue process that has
// bound the port, which would otherwise be able to intercept the plaintext
// credentials.
func sshdHostKeyCallback(paths []string) (ssh.HostKeyCallback, error) {
	var pinned []ssh.PublicKey
	var problems []string
	for _, path := range paths {
		keyBytes, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		pubKey, _, _, rest, err := ssh.ParseAuthorizedKey(keyBytes)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: parse: %v", path, err))
			continue
		}
		if len(bytes.TrimSpace(rest)) != 0 {
			problems = append(problems, fmt.Sprintf("%s: unexpected trailing data", path))
			continue
		}
		pinned = append(pinned, pubKey)
	}
	if len(pinned) == 0 {
		return nil, fmt.Errorf("no usable sshd host keys found: %s", strings.Join(problems, "; "))
	}

	callback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		presented := key.Marshal()
		for _, p := range pinned {
			if bytes.Equal(p.Marshal(), presented) {
				return nil
			}
		}
		return fmt.Errorf("ssh: host key mismatch: presented key of type %s is not among the %d pinned sshd host keys", key.Type(), len(pinned))
	}
	return callback, nil
}

func UserPwAuth(username string, passwd string) (bool, error) {
	/*
	 * mgmt-framework container does not have access to /etc/passwd, /etc/group,
	 * /etc/shadow and /etc/tacplus_conf files of host. One option is to share
	 * /etc of host with /etc of container. For now disable this and use ssh
	 * for authentication.
	 */
	// err := PAMAuthUser(username, passwd)

	// Pin the connection to the real local sshd's host key. This closes the
	// window where a rogue process bound to 127.0.0.1:22 could accept the
	// handshake and capture the plaintext credentials. Fails closed if the
	// pinned key(s) can't be loaded.
	hostKeyCallback, err := sshdHostKeyCallback(sshHostKeyPaths)
	if err != nil {
		// Distinct, higher-severity log: this is a host/config problem, not a
		// routine bad-password attempt, and (per fail-closed) blocks all auth.
		glog.Errorf("Authentication unavailable: cannot load sshd host key: %v", err)
		return false, err
	}

	//Use ssh for authentication.
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(passwd),
		},
		HostKeyCallback: hostKeyCallback,
	}

	// Dial and handshake manually so the timeout covers the entire handshake
	// and password-auth phase, not just the TCP connect (ssh.ClientConfig's
	// Timeout only bounds net.DialTimeout). Otherwise a rogue listener that
	// accepts the connection but stalls the handshake could hang the auth path.
	conn, err := net.DialTimeout("tcp", sshAuthAddr, sshAuthTimeout)
	if err != nil {
		glog.Infof("Authentication failed. user=%s, error:%s", username, err.Error())
		return false, err
	}
	if err := conn.SetDeadline(time.Now().Add(sshAuthTimeout)); err != nil {
		conn.Close()
		glog.Infof("Authentication failed. user=%s, error:%s", username, err.Error())
		return false, err
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, sshAuthAddr, config)
	if err != nil {
		conn.Close()
		glog.Infof("Authentication failed. user=%s, error:%s", username, err.Error())
		return false, err
	}
	// Handshake and auth succeeded; tear the connection down immediately.
	ssh.NewClient(sshConn, chans, reqs).Close()

	return true, nil
}
