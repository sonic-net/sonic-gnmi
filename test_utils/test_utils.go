package test_utils

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TestRedisRoot isolates the native fixture tree for a managed test run.
// The legacy Make suite retains its existing /var/run layout.
func TestRedisRoot() string {
	if root := os.Getenv("SONIC_TEST_REDIS_ROOT"); root != "" {
		return root
	}
	return "/var/run"
}

func TestRedisAddress() string {
	if address := os.Getenv("SONIC_TEST_REDIS_ADDRESS"); address != "" {
		return address
	}
	return "localhost:6379"
}

func SetupMultiNamespace() error {
	err := os.MkdirAll(filepath.Join(TestRedisRoot(), "redis0/sonic-db"), 0755)
	if err != nil {
		return err
	}
	srcFileName := [2]string{"../testdata/database_global.json", "../testdata/database_config_asic0.json"}
	dstFileName := [2]string{filepath.Join(TestRedisRoot(), "redis/sonic-db/database_global.json"), filepath.Join(TestRedisRoot(), "redis0/sonic-db/database_config_asic0.json")}
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
	err := os.Remove(filepath.Join(TestRedisRoot(), "redis/sonic-db/database_global.json"))
	if err != nil {
		return err
	}
	err = os.RemoveAll(filepath.Join(TestRedisRoot(), "redis0"))
	if err != nil {
		return err
	}
	return nil
}
func GetMultiNsNamespace() string {
	return "asic0"
}

func SetupMultiInstance() error {
	err := os.MkdirAll(filepath.Join(TestRedisRoot(), "redisdpu0/sonic-db"), 0755)
	if err != nil {
		return err
	}
	srcFileName := [2]string{"../testdata/database_global_dpu.json", "../testdata/database_config_dpu.json"}
	dstFileName := [2]string{filepath.Join(TestRedisRoot(), "redis/sonic-db/database_global.json"), filepath.Join(TestRedisRoot(), "redisdpu0/sonic-db/database_config.json")}
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

func CleanUpMultiInstance() error {
	err := os.Remove(filepath.Join(TestRedisRoot(), "redis/sonic-db/database_global.json"))
	if err != nil {
		return err
	}
	err = os.RemoveAll(filepath.Join(TestRedisRoot(), "redisdpu0"))
	if err != nil {
		return err
	}
	return nil
}

// PytestCommand uses the declared runner when Bazel manages the test runtime.
// Make keeps its ordinary pytest executable and environment.
func PytestCommand(directory, marker string) *exec.Cmd {
	executable := os.Getenv("SONIC_TEST_PYTEST")
	if executable == "" {
		executable = "pytest"
	}
	command := exec.Command(executable, "-m", marker)
	command.Dir = directory
	if os.Getenv("SONIC_TEST_REDIS_MANAGED") == "1" {
		for _, variable := range os.Environ() {
			if !strings.HasPrefix(variable, "LD_LIBRARY_PATH=") && !strings.HasPrefix(variable, "PYTHONHOME=") {
				command.Env = append(command.Env, variable)
			}
		}
	}
	return command
}
