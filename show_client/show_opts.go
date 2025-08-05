package show_client

import (
        sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const (
	SHOW_CMD_OPT_UNIMPLEMENTED_DESC  = "UNIMPLEMENTED"
	SHOW_CMD_OPT_DISPLAY_DESC        = "[display=all] Show internal interfaces [default: all]"
	SHOW_CMD_OPT_VERBOSE_DESC        = "[verbose] Enable verbose output"
	SHOW_CMD_OPT_INTERFACES_DESC     = "[interfaces=TEXT] Filter by interfaces name"
	SHOW_CMD_OPT_PERIOD_DESC         = "[period=INTEGER] Display statistics over a specified period (in seconds)"
	SHOW_CMD_OPT_JSON_DESC           = "[json] No-op since response is in json format"
)

var (
	SHOW_CMD_OPT_VERBOSE = sdc.ShowCmdOption{
		optName:     "verbose",
		optType:     sdc.Optional,
		description: SHOW_CMD_OPT_VERBOSE_DESC,
	}

	SHOW_CMD_OPT_NAMESPACE = sdc.ShowCmdOption{
		optName:     "namespace",
		optType:     sdc.Unimplemented,
		description: SHOW_CMD_OPT_UNIMPLEMENTED_DESC,
	}

	SHOW_CMD_OPT_DISPLAY = sdc.ShowCmdOption{
		optName:     "display",
		optType:     sdc.Optional,
		description: SHOW_CMD_OPT_DISPLAY_DESC,
	}

	SHOW_CMD_OPT_INTERFACES = sdc.ShowCmdOption{
		optName: "interfaces",
		optType: sdc.Optional,
		description: SHOW_CMD_OPT_INTERFACES_DESC,
	}

	SHOW_CMD_OPT_PERIOD = sdc.ShowCmdOption{
		optName:     "period",
		optType:     sdc.Optional,
		description: SHOW_CMD_OPT_PERIOD_DESC,
	}

	SHOW_CMD_OPT_JSON = ShowCmdOption{
		optName:     "json",
		optType:     sdc.Optional,
		description: SHOW_CMD_OPT_JSON_DESC,
	}
)
