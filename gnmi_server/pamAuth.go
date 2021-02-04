package gnmi

import (
	"github.com/Azure/sonic-telemetry/common_utils"
	"errors"
	"github.com/golang/glog"
	"github.com/msteinert/pam"
	"golang.org/x/crypto/ssh"
	"os/user"
)

type UserCredential struct {
	Username string
	Password string
}

//PAM conversation handler.
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

func UserPwAuth(username string, passwd string) (bool, error) {
	/*
	 * mgmt-framework container does not have access to /etc/passwd, /etc/group,
	 * /etc/shadow and /etc/tacplus_conf files of host. One option is to share
	 * /etc of host with /etc of container. For now disable this and use ssh
	 * for authentication.
	 */
	// err := PAMAuthUser(username, passwd)

	//Use ssh for authentication.
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.Password(passwd),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	c, err := ssh.Dial("tcp", "127.0.0.1:22", config)
	if err != nil {
		glog.Infof("Authentication failed. user=%s, error:%s", username, err.Error())
		return false, err
	}
	defer c.Conn.Close()

	return true, nil
}
