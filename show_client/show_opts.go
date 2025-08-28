package show_client

import (
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
)

const (
	showCmdOptionUnimplementedDesc = "UNIMPLEMENTED"
	showCmdOptionDisplayDesc       = "[display=all] No-op since no-multi-asic support"
	showCmdOptionVerboseDesc       = "[verbose=true] Enable verbose output"
	showCmdOptionInterfacesDesc    = "[interfaces=TEXT] Filter by interfaces name"
	showCmdOptionInterfaceDesc     = "[interface=TEXT] Filter by single interface name"
	showCmdOptionPortDesc          = "[port=TEXT] Filter by single port name"
	showCmdOptionDomDesc           = "[dom=false] Also display Digital Optical Monitoring (DOM) data"
	showCmdOptionPeriodDesc        = "[period=INTEGER] Display statistics over a specified period (in seconds)"
	showCmdOptionJsonDesc          = "[json=true] No-op since response is in json format"
	showCmdOptionDpuDesc           = "[dpu=TEXT] Filter by DPU module name"
)

var (
	showCmdOptionVerbose = sdc.NewShowCmdOption(
		"verbose",
		showCmdOptionVerboseDesc,
		sdc.BoolValue,
	)

	showCmdOptionNamespace = sdc.NewShowCmdOption(
		"namespace",
		showCmdOptionUnimplementedDesc,
		sdc.StringValue,
	)

	showCmdOptionDisplay = sdc.NewShowCmdOption(
		"display",
		showCmdOptionDisplayDesc,
		sdc.StringValue,
	)

	showCmdOptionInterfaces = sdc.NewShowCmdOption(
		"interfaces",
		showCmdOptionInterfacesDesc,
		sdc.StringSliceValue,
	)

	showCmdOptionPeriod = sdc.NewShowCmdOption(
		"period",
		showCmdOptionPeriodDesc,
		sdc.IntValue,
	)

	showCmdOptionJson = sdc.NewShowCmdOption(
		"json",
		showCmdOptionJsonDesc,
		sdc.BoolValue,
	)

	showCmdOptionInterface = sdc.NewShowCmdOption(
		"interface",
		showCmdOptionInterfaceDesc,
		sdc.StringValue,
	)

	showCmdOptionPort = sdc.NewShowCmdOption(
		"port",
		showCmdOptionPortDesc,
		sdc.StringValue,
	)

	showCmdOptionDom = sdc.NewShowCmdOption(
		"dom",
		showCmdOptionDomDesc,
		sdc.BoolValue,
	)

	showCmdOptionFetchFromHW = sdc.NewShowCmdOption(
		"fetch-from-hardware",
		showCmdOptionUnimplementedDesc,
		sdc.StringValue,
	)

	showCmdOptionDpu = sdc.NewShowCmdOption(
		"dpu",
		showCmdOptionDpuDesc,
		sdc.StringValue,
	)
)
