package show_client

import (
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

// All SHOW path and getters are defined here
func init() {
	sdc.RegisterCliPath(
		[]string{"SHOW", "reboot-cause"},
		getPreviousRebootCause,
		map[string]string {
			"history": "show/reboot-cause/history: Show history of reboot-cause",
		},
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "reboot-cause", "history"},
		getRebootCauseHistory,
		nil,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "clock"},
		getDate,
		map[string]string {
			"timezones": "show/clock/timezones: List of available timezones",
		},
		SHOW_CMD_OPT_VERBOSE,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "clock", "timezones"},
		getDateTimezone,
		nil,
		SHOW_CMD_OPT_VERBOSE,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "ipv6", "bgp", "summary"},
		getIPv6BGPSummary,
		nil,
		SHOW_CMD_OPT_NAMESPACE,
		SHOW_CMD_ARG_DISPLAY,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "interface", "counters"},
		getInterfaceCounters,
		nil,
		SHOW_CMD_OPT_NAMESPACE,
		SHOW_CMD_OPT_DISPLAY,
		SHOW_CMD_OPT_INTERFACES,
		SHOW_CMD_OPT_PERIOD,
		SHOW_CMD_OPT_JSON,
		SHOW_CMD_OPT_VERBOSE,
	)
}
