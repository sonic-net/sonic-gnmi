package gnmi

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	log "github.com/golang/glog"
)

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
	// os.CreateTemp requires the directory to exist. filepath.Dir(dstPath) must be a valid directory.
	tmpDst, err := os.CreateTemp(filepath.Dir(dstPath), filepath.Base(dstPath))
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmpDst, src); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(2).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := tmpDst.Close(); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(2).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	if err := os.Rename(tmpDst.Name(), dstPath); err != nil {
		if e := os.Remove(tmpDst.Name()); e != nil {
			log.V(2).Infof("Failed to cleanup file: %v: %v", tmpDst.Name(), e)
		}
		return err
	}
	return os.Chmod(dstPath, 0600)
}

func fileCheck(f string) error {
	srcStat, err := os.Lstat(f) // Use os.Lstat to check the file itself, not its target if it's a symlink.
	if err != nil {
		return err
	}
	if !srcStat.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", f)
	}
	return nil
}
