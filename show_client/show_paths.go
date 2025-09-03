package show_client

import (
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

// All SHOW path and getters are defined here
func init() {
	sdc.RegisterCliPath(
		[]string{"SHOW", "reboot-cause"},
		getPreviousRebootCause,
		map[string]string{
			"history": "show/reboot-cause/history: Show history of reboot-cause",
		},
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "reboot-cause", "history"},
		getRebootCauseHistory,
		nil,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "chassis", "modules", "status"},
		getChassisModuleStatus,
		nil,
		showCmdOptionDpu,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "chassis", "modules", "midplane-status"},
		getChassisModuleMidplaneStatus,
		nil,
		showCmdOptionDpu,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "system-health", "dpu"},
		getSystemHealthDpu,
		nil,
		showCmdOptionDpu,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "clock"},
		getDate,
		map[string]string{
			"timezones": "show/clock/timezones: List of available timezones",
		},
		showCmdOptionVerbose,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "clock", "timezones"},
		getDateTimezone,
		nil,
		showCmdOptionVerbose,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "ipv6", "bgp", "summary"},
		getIPv6BGPSummary,
		nil,
		sdc.UnimplementedOption(showCmdOptionNamespace),
		showCmdOptionDisplay,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "interface", "counters"},
		getInterfaceCounters,
		nil,
		sdc.UnimplementedOption(showCmdOptionNamespace),
		showCmdOptionDisplay,
		showCmdOptionInterfaces,
		showCmdOptionPeriod,
		showCmdOptionJson,
		showCmdOptionVerbose,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "interface", "errors"},
		getInterfaceErrors,
		nil,
		sdc.RequiredOption(showCmdOptionInterface),
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "interface", "fec", "status"},
		getInterfaceFecStatus,
		nil,
		showCmdOptionInterface,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "watermark", "telemetry", "interval"},
		getWatermarkTelemetryInterval,
		nil,
	)
	sdc.RegisterCliPath(
		[]string{"SHOW", "interface", "transceiver", "error-status"},
		getTransceiverErrorStatus,
		nil,
		showCmdOptionVerbose,
		sdc.UnimplementedOption(showCmdOptionNamespace),
		sdc.UnimplementedOption(showCmdOptionFetchFromHW),
		showCmdOptionInterface,
	)
}
