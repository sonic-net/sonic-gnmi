package show_client

type OptionType int

type ShowCmdOption struct{
	optName     string
	optType     OptionType // 0 means required, 1 means optional, -1 means unimplemented, all other values means invalid argument
	description string // will be used in help output
}
const (
	Required OptionType      = 0
	Optional OptionType      = 1
	Unimplemented OptionType = -1

	SHOW_CMD_OPT_GLOBAL_HELP_DESC    = "[help]Show this message"
	SHOW_CMD_OPT_UNIMPLEMENTED_DESC  = "UNIMPLEMENTED"
	SHOW_CMD_OPT_DISPLAY_DESC        = "[display=all] Show internal interfaces [default: all]"
	SHOW_CMD_OPT_VERBOSE_DESC        = "[verbose] Enable verbose output"
	SHOW_CMD_OPT_INTERFACES_DESC     = "[interfaces=TEXT] Filter by interfaces name"
	SHOW_CMD_OPT_PERIOD_DESC         = "[period=INTEGER] Display statistics over a specified period (in seconds)"
	SHOW_CMD_OPT_JSON_DESC           = "[json] No-op since response is in json format"
)

var (
	SHOW_CMD_OPT_GLOBAL_HELP = ShowCmdOption{ // No need to add this in RegisterCliPathWithOpts call as all paths will support
		optName: "help",
		optType: Optional,
		description: SHOW_CMD_OPT_GLOBAL_HELP_DESC,
	}

	SHOW_CMD_OPT_VERBOSE = ShowCmdOption{
		optName:     "verbose",
		optType:     Optional,
		description: SHOW_CMD_OPT_VERBOSE_DESC,
	}

	SHOW_CMD_OPT_NAMESPACE = ShowCmdOption{
		optName:     "namespace",
		optType:     Unimplemented,
		description: SHOW_CMD_OPT_UNIMPLEMENTED_DESC,
	}

	SHOW_CMD_OPT_DISPLAY = ShowCmdOption{
		optName:     "display",
		optType:     Optional,
		description: SHOW_CMD_OPT_DISPLAY_DESC,
	}

	SHOW_CMD_OPT_INTERFACES = ShowCmdOption{
		optName: "interfaces",
		optType: Optional,
		description: SHOW_CMD_OPT_INTERFACES_DESC,
	}

	SHOW_CMD_OPT_PERIOD = ShowCmdOption{
		optName:     "period",
		optType:     Optional,
		description: SHOW_CMD_OPT_PERIOD_DESC,
	}

	SHOW_CMD_OPT_JSON = ShowCmdOption{
		optName:     "json",
		optType:     Optional,
		description: SHOW_CMD_OPT_JSON_DESC,
	}
)
