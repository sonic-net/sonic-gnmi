package show_client

import (
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const (
	SHOW_CMD_OPT_UNIMPLEMENTED_DESC = "UNIMPLEMENTED"
	SHOW_CMD_OPT_DISPLAY_DESC       = "[display=all] Show internal interfaces [default: all]"
	SHOW_CMD_OPT_VERBOSE_DESC       = "[verbose=true] Enable verbose output"
	SHOW_CMD_OPT_INTERFACES_DESC    = "[interfaces=TEXT] Filter by interfaces name"
	SHOW_CMD_OPT_INTERFACE_DESC     = "[interface=TEXT] Required interface name"
	SHOW_CMD_OPT_PERIOD_DESC        = "[period=INTEGER] Display statistics over a specified period (in seconds)"
	SHOW_CMD_OPT_JSON_DESC          = "[json=true] No-op since response is in json format"
)

var (
	SHOW_CMD_OPT_VERBOSE = sdc.NewShowCmdOption(
		"verbose",
		sdc.Optional,
		SHOW_CMD_OPT_VERBOSE_DESC,
		sdc.BoolValue,
	)

	SHOW_CMD_OPT_NAMESPACE = sdc.NewShowCmdOption(
		"namespace",
		sdc.Unimplemented,
		SHOW_CMD_OPT_UNIMPLEMENTED_DESC,
		sdc.StringValue,
	)

	SHOW_CMD_OPT_DISPLAY = sdc.NewShowCmdOption(
		"display",
		sdc.Optional,
		SHOW_CMD_OPT_DISPLAY_DESC,
		sdc.StringValue,
	)

	SHOW_CMD_OPT_INTERFACES = sdc.NewShowCmdOption(
		"interfaces",
		sdc.Optional,
		SHOW_CMD_OPT_INTERFACES_DESC,
		sdc.StringSliceValue,
	)

	SHOW_CMD_OPT_PERIOD = sdc.NewShowCmdOption(
		"period",
		sdc.Optional,
		SHOW_CMD_OPT_PERIOD_DESC,
		sdc.IntValue,
	)

	SHOW_CMD_OPT_JSON = sdc.NewShowCmdOption(
		"json",
		sdc.Optional,
		SHOW_CMD_OPT_JSON_DESC,
		sdc.BoolValue,
	)

	SHOW_CMD_OPT_INTERFACE = sdc.NewShowCmdOption(
		"interface",
		sdc.Required,
		SHOW_CMD_OPT_INTERFACE_DESC,
		sdc.StringValue,
	)

	SHOW_CMD_OPT_INTERFACE_OPTIONAL = sdc.NewShowCmdOption(
		"interface",
		sdc.Optional,
		SHOW_CMD_OPT_INTERFACE_DESC,
		sdc.StringValue,
	)

	SHOW_CMD_OPT_FETCH_FROM_HW = sdc.NewShowCmdOption(
		"fetch-from-hardware",
		sdc.Unimplemented,
		SHOW_CMD_OPT_UNIMPLEMENTED_DESC,
		sdc.StringValue,
	)
)
