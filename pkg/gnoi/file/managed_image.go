package file

import (
	"context"
	"crypto/md5"
	"io"
	"os"
	"path/filepath"
	"strings"

	log "github.com/golang/glog"
	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/internal/imageexport"
)

func abortImage(upload *imageexport.Upload) {
	if err := upload.Abort(); err != nil {
		log.Errorf("Failed to remove managed-image staging artifact: %v", err)
	}
}

func managedImageDestination(path string) (bool, error) {
	if _, err := imageexport.Parts(path, false); err == nil {
		return true, nil
	}
	clean := filepath.Clean(path)
	if clean == imageexport.StoreRoot || strings.HasPrefix(clean, imageexport.StoreRoot+"/") {
		return false, os.ErrPermission
	}
	return false, nil
}

func downloadAndHashImage(ctx context.Context, url, logical, physical string) ([]byte, error) {
	managed, err := managedImageDestination(logical)
	if err != nil {
		return nil, err
	}
	body, _, err := download.DownloadHTTPStreaming(ctx, url, maxFileSize)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	var target *os.File
	var upload *imageexport.Upload
	if managed {
		root, err := readPathRoot()
		if err != nil {
			return nil, err
		}
		upload, err = (imageexport.Store{HostRoot: root}).Create(logical)
		if err != nil {
			return nil, err
		}
		defer abortImage(upload)
		target = upload.File
	} else {
		// Authorized upgrade/configuration writes are not file-read exports.
		if err := os.MkdirAll(filepath.Dir(physical), 0755); err != nil {
			return nil, err
		}
		target, err = os.OpenFile(physical, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return nil, err
		}
		defer target.Close()
	}
	hasher := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	if _, err := io.Copy(io.MultiWriter(target, hasher), body); err != nil {
		if upload == nil {
			if removeErr := os.Remove(physical); removeErr != nil {
				log.Errorf("Failed to remove incomplete transfer: %v", removeErr)
			}
		}
		return nil, err
	}
	if upload != nil {
		if err := upload.Commit(); err != nil {
			return nil, err
		}
	} else if err := target.Close(); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}
