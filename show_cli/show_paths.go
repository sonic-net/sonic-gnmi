package show_cli

import (
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

// All SHOW path and getters are defined here
func init() {
	sdc.RegisterCliPath(
		[]string{"SHOW", "reboot-cause"},
		getPreviousRebootCause,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "reboot-cause", "history"},
		getRebootCauseHistory,
	)
}
