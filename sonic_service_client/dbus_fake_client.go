package host_service

import "errors"

// FakeClient is a mock implementation of the Service interface.
type FakeClient struct{}

func (f *FakeClient) Close() error                         { return nil }
func (f *FakeClient) ConfigReload(fileName string) error   { return nil }
func (f *FakeClient) ConfigReplace(fileName string) error  { return nil }
func (f *FakeClient) ConfigSave(fileName string) error     { return nil }
func (f *FakeClient) ApplyPatchYang(fileName string) error { return nil }
func (f *FakeClient) ApplyPatchDb(fileName string) error   { return nil }
func (f *FakeClient) CreateCheckPoint(cpName string) error { return nil }
func (f *FakeClient) DeleteCheckPoint(cpName string) error { return nil }
func (f *FakeClient) StopService(service string) error     { return nil }
func (f *FakeClient) RestartService(service string) error  { return nil }
func (f *FakeClient) GetFileStat(path string) (map[string]string, error) {
	return map[string]string{
		"path":          path,
		"last_modified": "1686999999", // any valid Unix timestamp
		"permissions":   "644",
		"size":          "100",
		"umask":         "022",
	}, nil
}
func (f *FakeClient) DownloadFile(host, username, password, remotePath, localPath, protocol string) error {
	return nil
}
func (f *FakeClient) RemoveFile(path string) error                   { return nil }
func (f *FakeClient) DownloadImage(url string, save_as string) error { return nil }
func (f *FakeClient) InstallImage(where string) error                { return nil }
func (f *FakeClient) ListImages() (string, error)                    { return "image1", nil }
func (f *FakeClient) ActivateImage(image string) error               { return nil }
func (f *FakeClient) LoadDockerImage(image string) error             { return nil }
func (f *FakeClient) FactoryReset(cmd string) (string, error) {
	if cmd == "" {
		return "", errors.New("Previous reset is ongoing")
	}
	return cmd, nil
}
func (f *FakeClient) InstallOS(req string) (string, error) {
	if req == "" {
		return "", errors.New("invalid OS install request")
	}
	return req, nil
}

var _ Service = &FakeClient{}

// FakeClientWithError simulates failure in specific methods.
type FakeClientWithError struct {
	FakeClient
}

func (f *FakeClientWithError) RemoveFile(path string) error {
	return errors.New("simulated failure")
}
