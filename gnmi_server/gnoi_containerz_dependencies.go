package gnmi

import (
	"os"

	"github.com/sonic-net/sonic-gnmi/internal/download"
	"github.com/sonic-net/sonic-gnmi/pkg/hostfs"
	ssc "github.com/sonic-net/sonic-gnmi/sonic_service_client"
)

func defaultContainerzDeployDependencies() containerzDeployDependencies {
	return containerzDeployDependencies{
		authenticate: authenticate,
		createTempFile: func(directory, pattern string) (containerzTemporaryFile, error) {
			return os.CreateTemp(directory, pattern)
		},
		downloadRemote: download.DownloadRemote,
		newImageLoader: func() (containerzImageLoader, error) {
			return ssc.NewDbusClient()
		},
		removeFile:        os.Remove,
		translateHostPath: hostfs.Translate,
	}
}
