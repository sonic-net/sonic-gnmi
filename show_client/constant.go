package show_client

var QsfPDataMap = map[string]string{
	"model":                     "Vendor PN",
	"vendor_oui":                "Vendor OUI",
	"vendor_date":               "Vendor Date Code(YYYY-MM-DD Lot)",
	"manufacturer":              "Vendor Name",
	"vendor_rev":                "Vendor Rev",
	"serial":                    "Vendor SN",
	"type":                      "Identifier",
	"ext_identifier":            "Extended Identifier",
	"ext_rateselect_compliance": "Extended RateSelect Compliance",
	"cable_length":              "cable_length",
	"cable_type":                "Length",
	"nominal_bit_rate":          "Nominal Bit Rate(100Mbs)",
	"specification_compliance":  "Specification compliance",
	"encoding":                  "Encoding",
	"connector":                 "Connector",
	"application_advertisement": "Application Advertisement",
}

var QsfpCmisDeltaDataMap = map[string]string{
	"host_lane_count":            "Host Lane Count",
	"media_lane_count":           "Media Lane Count",
	"active_apsel_hostlane1":     "Active application selected code assigned to host lane 1",
	"active_apsel_hostlane2":     "Active application selected code assigned to host lane 2",
	"active_apsel_hostlane3":     "Active application selected code assigned to host lane 3",
	"active_apsel_hostlane4":     "Active application selected code assigned to host lane 4",
	"active_apsel_hostlane5":     "Active application selected code assigned to host lane 5",
	"active_apsel_hostlane6":     "Active application selected code assigned to host lane 6",
	"active_apsel_hostlane7":     "Active application selected code assigned to host lane 7",
	"active_apsel_hostlane8":     "Active application selected code assigned to host lane 8",
	"media_interface_technology": "Media Interface Technology",
	"hardware_rev":               "Module Hardware Rev",
	"cmis_rev":                   "CMIS Rev",
	"active_firmware":            "Active Firmware",
	"inactive_firmware":          "Inactive Firmware",
	"e1_active_firmware":         "E1 Active Firmware",
	"e1_inactive_firmware":       "E1 Inactive Firmware",
	"e1_server_firmware":         "E1 Server Firmware",
	"e2_active_firmware":         "E2 Active Firmware",
	"e2_inactive_firmware":       "E2 Inactive Firmware",
	"e2_server_firmware":         "E2 Server Firmware",
}

var CCmisDeltaDataMap = map[string]string{
	"supported_max_tx_power":   "Supported Max TX Power",
	"supported_min_tx_power":   "Supported Min TX Power",
	"supported_max_laser_freq": "Supported Max Laser Frequency",
	"supported_min_laser_freq": "Supported Min Laser Frequency",
}

var CmisDomChannelMonitorMap = map[string]string{
	"rx1power": "RX1Power",
	"rx2power": "RX2Power",
	"rx3power": "RX3Power",
	"rx4power": "RX4Power",
	"rx5power": "RX5Power",
	"rx6power": "RX6Power",
	"rx7power": "RX7Power",
	"rx8power": "RX8Power",
	"tx1bias":  "TX1Bias",
	"tx2bias":  "TX2Bias",
	"tx3bias":  "TX3Bias",
	"tx4bias":  "TX4Bias",
	"tx5bias":  "TX5Bias",
	"tx6bias":  "TX6Bias",
	"tx7bias":  "TX7Bias",
	"tx8bias":  "TX8Bias",
	"tx1power": "TX1Power",
	"tx2power": "TX2Power",
	"tx3power": "TX3Power",
	"tx4power": "TX4Power",
	"tx5power": "TX5Power",
	"tx6power": "TX6Power",
	"tx7power": "TX7Power",
	"tx8power": "TX8Power",
}

var QsfpDdDomValueUnitMap = map[string]string{
	"rx1power":    "dBm",
	"rx2power":    "dBm",
	"rx3power":    "dBm",
	"rx4power":    "dBm",
	"rx5power":    "dBm",
	"rx6power":    "dBm",
	"rx7power":    "dBm",
	"rx8power":    "dBm",
	"tx1bias":     "mA",
	"tx2bias":     "mA",
	"tx3bias":     "mA",
	"tx4bias":     "mA",
	"tx5bias":     "mA",
	"tx6bias":     "mA",
	"tx7bias":     "mA",
	"tx8bias":     "mA",
	"tx1power":    "dBm",
	"tx2power":    "dBm",
	"tx3power":    "dBm",
	"tx4power":    "dBm",
	"tx5power":    "dBm",
	"tx6power":    "dBm",
	"tx7power":    "dBm",
	"tx8power":    "dBm",
	"temperature": "C",
	"voltage":     "Volts",
}

var QsfpDomChannelMonitorMap = map[string]string{
	"rx1power": "RX1Power",
	"rx2power": "RX2Power",
	"rx3power": "RX3Power",
	"rx4power": "RX4Power",
	"tx1bias":  "TX1Bias",
	"tx2bias":  "TX2Bias",
	"tx3bias":  "TX3Bias",
	"tx4bias":  "TX4Bias",
	"tx1power": "TX1Power",
	"tx2power": "TX2Power",
	"tx3power": "TX3Power",
	"tx4power": "TX4Power",
}

var DomValueUnitMap = map[string]string{
	"rx1power":    "dBm",
	"rx2power":    "dBm",
	"rx3power":    "dBm",
	"rx4power":    "dBm",
	"tx1bias":     "mA",
	"tx2bias":     "mA",
	"tx3bias":     "mA",
	"tx4bias":     "mA",
	"tx1power":    "dBm",
	"tx2power":    "dBm",
	"tx3power":    "dBm",
	"tx4power":    "dBm",
	"temperature": "C",
	"voltage":     "Volts",
}

var SfpDomChannelThresholdMap = map[string]string{
	"txpowerhighalarm":   "TxPowerHighAlarm",
	"txpowerlowalarm":    "TxPowerLowAlarm",
	"txpowerhighwarning": "TxPowerHighWarning",
	"txpowerlowwarning":  "TxPowerLowWarning",
	"rxpowerhighalarm":   "RxPowerHighAlarm",
	"rxpowerlowalarm":    "RxPowerLowAlarm",
	"rxpowerhighwarning": "RxPowerHighWarning",
	"rxpowerlowwarning":  "RxPowerLowWarning",
	"txbiashighalarm":    "TxBiasHighAlarm",
	"txbiaslowalarm":     "TxBiasLowAlarm",
	"txbiashighwarning":  "TxBiasHighWarning",
	"txbiaslowwarning":   "TxBiasLowWarning",
}

var QsfpDomChannelThresholdMap = map[string]string{
	"rxpowerhighalarm":   "RxPowerHighAlarm",
	"rxpowerlowalarm":    "RxPowerLowAlarm",
	"rxpowerhighwarning": "RxPowerHighWarning",
	"rxpowerlowwarning":  "RxPowerLowWarning",
	"txbiashighalarm":    "TxBiasHighAlarm",
	"txbiaslowalarm":     "TxBiasLowAlarm",
	"txbiashighwarning":  "TxBiasHighWarning",
	"txbiaslowwarning":   "TxBiasLowWarning",
}

var DomChannelThresholdUnitMap = map[string]string{
	"txpowerhighalarm":   "dBm",
	"txpowerlowalarm":    "dBm",
	"txpowerhighwarning": "dBm",
	"txpowerlowwarning":  "dBm",
	"rxpowerhighalarm":   "dBm",
	"rxpowerlowalarm":    "dBm",
	"rxpowerhighwarning": "dBm",
	"rxpowerlowwarning":  "dBm",
	"txbiashighalarm":    "mA",
	"txbiaslowalarm":     "mA",
	"txbiashighwarning":  "mA",
	"txbiaslowwarning":   "mA",
}

var DomModuleMonitorMap = map[string]string{
	"temperature": "Temperature",
	"voltage":     "Vcc",
}

var DomModuleThresholdMap = map[string]string{
	"temphighalarm":   "TempHighAlarm",
	"templowalarm":    "TempLowAlarm",
	"temphighwarning": "TempHighWarning",
	"templowwarning":  "TempLowWarning",
	"vcchighalarm":    "VccHighAlarm",
	"vcclowalarm":     "VccLowAlarm",
	"vcchighwarning":  "VccHighWarning",
	"vcclowwarning":   "VccLowWarning",
}

var DomModuleThresholdUnitMap = map[string]string{
	"temphighalarm":   "C",
	"templowalarm":    "C",
	"temphighwarning": "C",
	"templowwarning":  "C",
	"vcchighalarm":    "Volts",
	"vcclowalarm":     "Volts",
	"vcchighwarning":  "Volts",
	"vcclowwarning":   "Volts",
}

var SfpDomChannelMonitorMap = map[string]string{
	"rx1power": "RXPower",
	"tx1bias":  "TXBias",
	"tx1power": "TXPower",
}
