package gnmi

import (
	"io/ioutil"
	"os/exec"
	"reflect"
	"testing"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"

	"github.com/agiledragon/gomonkey/v2"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const (
	ServerPort        = 8081
	ApplDbNum         = 0
	AsicDbNum         = 1
	CountersDbNum     = 2
	ConfigDbNum       = 4
	StateDbNum        = 6
	ChassisStateDbNum = 13

	TargetAddr   = "127.0.0.1:8081"
	QueryTimeout = 10
)

func MockNSEnterBGPSummary(t *testing.T, fileName string) *gomonkey.Patches {
	fileContentBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file %v err: %v", fileName, err)
	}
	patches := gomonkey.ApplyFunc(exec.Command, func(name string, args ...string) *exec.Cmd {
		return &exec.Cmd{}
	})
	patches.ApplyMethod(reflect.TypeOf(&exec.Cmd{}), "CombinedOutput", func(_ *exec.Cmd) ([]byte, error) {
		return fileContentBytes, nil
	})
	return patches
}

func MockReadFile(fileName string, fileContent string, fileReadErr error) {
	sdc.ImplIoutilReadFile = func(filePath string) ([]byte, error) {
		if filePath == fileName {
			if fileReadErr != nil {
				return nil, fileReadErr
			}
			return []byte(fileContent), nil
		}
		return ioutil.ReadFile(filePath)
	}
}

func FlushDataSet(t *testing.T, dbNum int) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, dbNum, ns)
	defer rclient.Close()
	rclient.FlushDB()
}

func AddDataSet(t *testing.T, dbNum int, fileName string) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	rclient := getRedisClientN(t, dbNum, ns)
	defer rclient.Close()

	fileContentBytes, err := ioutil.ReadFile(fileName)
	if err != nil {
		t.Fatalf("read file: %v, err: %v", fileName, err)
	}

	fileContent := loadConfig(t, "", fileContentBytes)
	loadDB(t, rclient, fileContent)
}

func ResetDataSetsAndMappings(t *testing.T) {
	FlushDataSet(t, ApplDbNum)
	FlushDataSet(t, AsicDbNum)
	FlushDataSet(t, CountersDbNum)
	FlushDataSet(t, ConfigDbNum)
	FlushDataSet(t, StateDbNum)
	sdc.ClearMappings()
}
