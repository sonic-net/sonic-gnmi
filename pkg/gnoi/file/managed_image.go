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

func downloadAndHashImage(ctx context.Context, url, logical string) ([]byte, error) {
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
	var legacy *legacyUpload
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
		legacy, err = createLegacyUpload(logical)
		if err != nil {
			return nil, err
		}
		defer legacy.Abort()
		target = legacy.File
	}
	hasher := md5.New() // nosemgrep: go.lang.security.audit.crypto.use_of_weak_crypto.use-of-md5
	if _, err := io.Copy(io.MultiWriter(target, hasher), body); err != nil {
		return nil, err
	}
	if upload != nil {
		if err := upload.Commit(); err != nil {
			return nil, err
		}
	} else if err := legacy.Commit(legacy.permissions); err != nil {
		return nil, err
	}
	return hasher.Sum(nil), nil
}
