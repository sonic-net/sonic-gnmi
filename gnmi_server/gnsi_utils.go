package gnmi

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"strings"
	"sync"
	//"time"

	//"github.com/sonic-net/sonic-gnmi/crypta_client/pkg/tpm"
	//"github.com/sonic-net/sonic-gnmi/crypta_client/pkg/transport/usb"
	lvl "github.com/sonic-net/sonic-gnmi/gnmi_server/log"

	log "github.com/golang/glog"
	//"github.com/google/go-tpm/tpm2"
	//"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Azure/sonic-mgmt-common/translib/db"
	"github.com/redis/go-redis/v9"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const (
	credentialsTbl string = "CREDENTIALS"
)

var (
	// Mutex for DB writes
	dbWriteMutex sync.Mutex
)

/*
*
const (

	ekIndex         uint32 = 0x01c00050
	ekTemplateIndex uint32 = 0x01c00051
	ekChainIndex    uint32 = 0x01c00100

)

	func createAKPrimary(tpmClient *tpm.TPM, akTemplate []byte) (*tpm2.CreatePrimaryResponse, error) {
		log.V(lvl.INFO).Infof("createAKPrimary")
		if tpmClient == nil {
			return nil, fmt.Errorf("tpmClient is nil")
		}
		template, err := tpm2.Unmarshal[tpm2.TPMTPublic](akTemplate)
		if err != nil {
			log.V(lvl.ERROR).Infof("Unmarshal failed: %v\n\n", err)
		}
		log.V(lvl.INFO).Infof("template: %#v\n\n", template)

		return tpm2.CreatePrimary{
			PrimaryHandle: tpm2.TPMRHFWOwner,
			InPublic:      tpm2.New2B(*template),
			CreationPCR:   tpm2.TPMLPCRSelection{},
		}.Execute(tpmClient)
	}

	func createEKPrimary(tpmClient *tpm.TPM, ekTemplate *tpm2.NVReadResponse) (*tpm2.CreatePrimaryResponse, error) {
		log.V(lvl.INFO).Infof("createEKPrimary")
		if tpmClient == nil {
			return nil, fmt.Errorf("tpmClient is nil")
		}
		template, err := tpm2.Unmarshal[tpm2.TPMTPublic](ekTemplate.Data.Buffer)
		if err != nil {
			log.V(lvl.ERROR).Infof("Unmarshal failed: %v\n\n", err)
		}
		log.V(lvl.INFO).Infof("template: %#v\n\n", template)
		return tpm2.CreatePrimary{
			PrimaryHandle: tpm2.TPMRHFWEndorsement,
			InPublic:      tpm2.New2B(*template),
			CreationPCR:   tpm2.TPMLPCRSelection{},
		}.Execute(tpmClient)

}

	func readEKCert(tpmClient *tpm.TPM) (*tpm2.NVReadResponse, error) {
		// Get the Handle for the EK
		log.V(lvl.INFO).Infof("readEKCert")
		if tpmClient == nil {
			return nil, fmt.Errorf("tpmClient is nil")
		}
		log.V(lvl.INFO).Infof("readEKCert DEBUG: %+v", tpmClient)
		ekHandle, err := tpm2.NVReadPublic{
			NVIndex: tpm2.TPMHandle(ekIndex),
		}.Execute(tpmClient)
		if err != nil {
			return nil, fmt.Errorf("NVReadPublic failed: %v", err)
		}
		log.V(lvl.INFO).Infof("ekHandle: %#v", ekHandle)

		ekCont, err := ekHandle.NVPublic.Contents()
		if err != nil {
			return nil, fmt.Errorf("NVReadPublic failed: %v", err)
		}
		log.V(lvl.INFO).Infof("ekCont: %#v", ekCont)

		ekNamedHandle := tpm2.NamedHandle{Handle: tpm2.TPMHandle(ekIndex), Name: ekHandle.NVName}
		return tpm2.NVRead{
			AuthHandle: tpm2.AuthHandle{
				Handle: ekNamedHandle.Handle,
				Name:   ekNamedHandle.Name,
				Auth:   tpm2.HMAC(tpm2.TPMAlgSHA256, 16, tpm2.Auth([]byte{})),
			},
			NVIndex: ekNamedHandle,
			Size:    ekCont.DataSize,
			Offset:  0,
		}.Execute(tpmClient)
	}

	func readEKChain(tpmClient *tpm.TPM) (*tpm2.NVReadResponse, error) {
		// Get the Handle for the EK Chain
		log.V(lvl.INFO).Infof("readEKChain")
		if tpmClient == nil {
			return nil, fmt.Errorf("tpmClient is nil")
		}
		ekChainHandle, err := tpm2.NVReadPublic{
			NVIndex: tpm2.TPMHandle(ekChainIndex),
		}.Execute(tpmClient)
		if err != nil {
			return nil, fmt.Errorf("NVReadPublic cert failed: %v", err)
		}

		ekChainCont, err := ekChainHandle.NVPublic.Contents()

		ekChainNamedHandle := tpm2.NamedHandle{Handle: tpm2.TPMHandle(ekChainIndex), Name: ekChainHandle.NVName}
		return tpm2.NVRead{
			AuthHandle: tpm2.AuthHandle{
				Handle: ekChainNamedHandle.Handle,
				Name:   ekChainNamedHandle.Name,
				Auth:   tpm2.HMAC(tpm2.TPMAlgSHA256, 16, tpm2.Auth([]byte{})),
			},
			NVIndex: ekChainNamedHandle,
			Size:    ekChainCont.DataSize,
			Offset:  0,
		}.Execute(tpmClient)
	}

	func readEKTemp(tpmClient *tpm.TPM) (*tpm2.NVReadResponse, error) {
		log.V(lvl.INFO).Infof("readEKTemp")
		if tpmClient == nil {
			return nil, fmt.Errorf("tpmClient is nil")
		}
		ekTempHandle, err := tpm2.NVReadPublic{
			NVIndex: tpm2.TPMHandle(ekTemplateIndex),
		}.Execute(tpmClient)
		if err != nil {
			return nil, fmt.Errorf("NVReadPublic cert failed: %v", err)
		}

		ekTempCont, err := ekTempHandle.NVPublic.Contents()
		if err != nil {
			return nil, fmt.Errorf("NVReadPublic cert failed: %v", err)
		}
		log.V(lvl.INFO).Infof("ekTempCont err: %v contents: %#v", err, ekTempCont)

		ekTempNamedHandle := tpm2.NamedHandle{Handle: tpm2.TPMHandle(ekTemplateIndex), Name: ekTempHandle.NVName}
		return tpm2.NVRead{
			AuthHandle: tpm2.AuthHandle{
				Handle: ekTempNamedHandle.Handle,
				Name:   ekTempNamedHandle.Name,
				Auth:   tpm2.HMAC(tpm2.TPMAlgSHA256, 16, tpm2.Auth([]byte{})),
			},
			NVIndex: ekTempNamedHandle,
			Size:    ekTempCont.DataSize,
			Offset:  0,
		}.Execute(tpmClient)
	}

	func flushHandle(t *tpm.TPM, h tpm2.TPMHandle) {
		log.V(lvl.INFO).Infof("flushHandle: %+v", h)
		if t == nil {
			log.V(lvl.ERROR).Infof("tpmClient is nil")
			return
		}
		_, err := tpm2.FlushContext{
			FlushHandle: h,
		}.Execute(t)
		if err != nil {
			log.V(lvl.ERROR).Infof("Failed to flush handle %v", h)
		}
	}

	func startupTPM() (*tpm.TPM, error) {
		// Create TPM object
		log.V(lvl.INFO).Infof("startupTPM")
		d, err := usb.New(usb.Options{
			VendorID:  usb.VendorIDGoogle,
			ProductID: usb.ProductIDDauntless,
		})
		if err != nil {
			return nil, err
		}
		tpm := tpm.New(d)

		// TPM startup
		_, resp := tpm2.Startup{
			StartupType: tpm2.TPMSUClear,
		}.Execute(tpm)
		return tpm, resp
	}

*
*/
func copyFile(srcPath, dstPath string) error {
	srcStat, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", srcPath)
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	tmpDst, err := os.CreateTemp(filepath.Dir(dstPath), filepath.Base(dstPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmpDst, src); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(lvl.WARNING).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := tmpDst.Close(); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(lvl.WARNING).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := os.Rename(tmpDst.Name(), dstPath); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(lvl.WARNING).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	return os.Chmod(dstPath, 0600)
}

func attemptWrite(name string, data []byte, perm os.FileMode) error {
	err := os.WriteFile(name, data, perm)
	if err != nil {
		if e := os.Remove(name); e != nil {
			err = fmt.Errorf("Write %s failed: %w; Cleanup failed", name, err)
		}
	}
	return err
}

// getKey generates the hash key from the supplied string array.
func getKey(k []string) string {
	return strings.Join(k, "|")
}

// writeCredentialsMetadataToDB writes the credentials freshness data to the DB.
func writeCredentialsMetadataToDB(tbl, key, fld, val string) error {
	sc, err := getRedisDBClient(stateDB)
	if err != nil {
		log.V(lvl.ERROR).Info(err.Error())
		return fmt.Errorf("REDIS is not available.")
	}
	defer db.CloseRedisClient(sc)
	// Write metadata.
	path := getKey([]string{credentialsTbl, tbl})
	if len(key) > 0 {
		path = getKey([]string{path, key})
	}
	dbWriteMutex.Lock()
	err = sc.HSet(context.Background(), path, fld, val).Err()
	dbWriteMutex.Unlock()
	if err != nil {
		log.V(lvl.ERROR).Infof("Cannot write credentials metadata to the DB. [path:'%v', fld:'%v', val:'%v']", path, fld, val)
		return err
	}
	log.V(lvl.DEBUG).Infof("Successfully wrote credentials metadata to the DB. [path:'%v', fld:'%v', val:'%v']", path, fld, val)
	return nil
}
func fileCheck(f string) error {
	srcStat, err := os.Stat(f)
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", f)
	}
	return nil
}

// Creates and returns a new REDIS client for the supplied DB.
func getRedisDBClient(dbName string) (*redis.Client, error) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, _ := sdcfg.GetDbTcpAddr(dbName, ns)
	id, _ := sdcfg.GetDbId(dbName, ns)
	rclient := db.TransactionalRedisClientWithOpts(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          id,
		DialTimeout: 0,
	})
	if rclient == nil {
		return nil, fmt.Errorf("Cannot create redis client.")
	}
	if _, err := rclient.Ping(context.Background()).Result(); err != nil {
		return nil, err
	}
	return rclient, nil
}
