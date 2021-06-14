package test_utils

import (
	"io"
	"os"
)

func SetupMultiNamespace() error {
	err := os.MkdirAll("/var/run/redis0/sonic-db/", 0755)
	if err != nil {
		return err
	}
	srcFileName := [2]string{"../testdata/database_global.json", "../testdata/database_config_asic0.json"}
	dstFileName := [2]string{"/var/run/redis/sonic-db/database_global.json", "/var/run/redis0/sonic-db/database_config_asic0.json"}
	for i := 0; i < len(srcFileName); i++ {
		sourceFileStat, err := os.Stat(srcFileName[i])
		if err != nil {
			return err
		}

		if !sourceFileStat.Mode().IsRegular() {
			return err
		}

		source, err := os.Open(srcFileName[i])
		if err != nil {
			return err
		}
		defer source.Close()

		destination, err := os.Create(dstFileName[i])
		if err != nil {
			return err
		}
		defer destination.Close()
		_, err = io.Copy(destination, source)
		if err != nil {
			return err
		}
	}
	return nil
}

func CleanUpMultiNamespace() error {
	err := os.Remove("/var/run/redis/sonic-db/database_global.json")
	if err != nil {
		return err
	}
	err = os.RemoveAll("/var/run/redis0")
	if err != nil {
		return err
	}
	return nil
}
func GetMultiNsNamespace() string {
	return "asic0"
}
