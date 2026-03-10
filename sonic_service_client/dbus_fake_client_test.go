package host_service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFakeClientMethods(t *testing.T) {
	client := &FakeClient{
		Command: make(chan []string, 10),
	}

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

	output, err = client.HealthzCollect("collect-event")
	assert.NoError(t, err)
	assert.Equal(t, "/tmp/dump/fake-collect-success", output)
	output, err = client.HealthzCollect("")
	assert.Error(t, err)
	assert.Equal(t, "", output)
	assert.Equal(t, "request cannot be empty", err.Error())

	output, err = client.HealthzCheck("check-event")
	assert.NoError(t, err)
	assert.Equal(t, "fake-check-success", output)
	output, err = client.HealthzCheck("")
	assert.Error(t, err)
	assert.Equal(t, "", output)
	assert.Equal(t, "request cannot be empty", err.Error())

	output, err = client.HealthzAck("ack-event")
	assert.NoError(t, err)
	assert.Equal(t, "fake-ack-success", output)
	output, err = client.HealthzAck("")
	assert.Error(t, err)
	assert.Equal(t, "", output)
	assert.Equal(t, "request cannot be empty", err.Error())

	// --- Credentialz Fake Tests ---

	t.Run("SSHCheckpoint", func(t *testing.T) {
		err := client.SSHCheckpoint(CredzCPCreate)
		assert.NoError(t, err)
		msg := <-client.Command
		assert.Equal(t, []string{"ssh_mgmt.create_checkpoint", ""}, msg)
	})

	t.Run("SSHMgmtSet", func(t *testing.T) {
		testCmd := `{"SshAccountKeys": []}`
		err := client.SSHMgmtSet(testCmd)
		assert.NoError(t, err)
		msg := <-client.Command
		assert.Equal(t, []string{"ssh_mgmt.set", testCmd}, msg)
	})

	t.Run("ConsoleCheckpoint", func(t *testing.T) {
		err := client.ConsoleCheckpoint(CredzCPRestore)
		assert.NoError(t, err)
		msg := <-client.Command
		assert.Equal(t, []string{"gnsi_console.restore_checkpoint", ""}, msg)
	})

	t.Run("ConsoleSet", func(t *testing.T) {
		testCmd := `{"ConsolePasswords": []}`
		err := client.ConsoleSet(testCmd)
		assert.NoError(t, err)
		msg := <-client.Command
		assert.Equal(t, []string{"gnsi_console.set", testCmd}, msg)
	})

	t.Run("GLOMEConfigSet", func(t *testing.T) {
		testCmd := `{"enabled": true}`
		err := client.GLOMEConfigSet(context.Background(), testCmd)
		assert.NoError(t, err)
		msg := <-client.Command
		assert.Equal(t, []string{"glome.push_config", testCmd}, msg)
	})

	t.Run("GLOMERestoreCheckpoint", func(t *testing.T) {
		err := client.GLOMERestoreCheckpoint(context.Background())
		assert.NoError(t, err)
		msg := <-client.Command
		assert.Equal(t, []string{"glome.restore_checkpoint", ""}, msg)
	})
}
