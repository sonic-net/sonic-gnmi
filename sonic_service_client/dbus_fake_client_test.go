package host_service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFakeClientMethods(t *testing.T) {
	client := &FakeClient{}

	assert.NoError(t, client.Close())
	assert.NoError(t, client.ConfigReload("test.conf"))
	assert.NoError(t, client.ConfigReplace("replace.conf"))
	assert.NoError(t, client.ConfigSave("save.conf"))
	assert.NoError(t, client.ApplyPatchYang("yang.patch"))
	assert.NoError(t, client.ApplyPatchDb("db.patch"))
	assert.NoError(t, client.CreateCheckPoint("cp1"))
	assert.NoError(t, client.DeleteCheckPoint("cp1"))
	assert.NoError(t, client.StopService("swss"))
	assert.NoError(t, client.RestartService("bgp"))

	stat, err := client.GetFileStat("/etc/sonic/config_db.json")
	assert.NoError(t, err)
	assert.Equal(t, "022", stat["umask"])

	assert.NoError(t, client.DownloadFile("host", "user", "pass", "/remote", "/local", "scp"))
	assert.NoError(t, client.RemoveFile("/tmp/test"))
	assert.NoError(t, client.DownloadImage("http://example.com/image", "image.bin"))
	assert.NoError(t, client.InstallImage("ONIE"))
	img, err := client.ListImages()
	assert.NoError(t, err)
	assert.Equal(t, "image1", img)
	assert.NoError(t, client.ActivateImage("image1"))
	assert.NoError(t, client.LoadDockerImage("docker-image"))

	output, err := client.FactoryReset("REBOOT")
	assert.NoError(t, err)
	assert.Equal(t, "REBOOT", output)

	output, err = client.FactoryReset("")
	assert.Error(t, err)
	assert.Equal(t, "", output)
	assert.Equal(t, "Previous reset is ongoing", err.Error())

	output, err = client.InstallOS("stable")
	assert.NoError(t, err)
	assert.Equal(t, "stable", output)

	output, err = client.InstallOS("")
	assert.Error(t, err)
	assert.Equal(t, "", output)
	assert.Equal(t, "invalid OS install request", err.Error())
}
