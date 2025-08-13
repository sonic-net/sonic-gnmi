package client

type OptionType int
type ValueType int

type ShowCmdOption struct {
	optName     string
	optType     OptionType // 0 means required, 1 means optional, -1 means unimplemented, all other values means invalid argument
	description string     // will be used in help output
	valueType   ValueType
}

type OptionValue struct {
	value interface{}
}

type OptionMap map[string]OptionValue

type DataGetter func(options OptionMap) ([]byte, error)

type TablePath = tablePath

type ShowPathConfig struct {
	dataGetter  DataGetter
	options     map[string]ShowCmdOption
	description map[string]map[string]string
}

var (
	showCmdOptionHelp = NewShowCmdOption(
		"help",
		showCmdOptionHelpDesc,
		BoolValue,
	)
)

const (
	StringValue      ValueType = 0
	StringSliceValue ValueType = 1
	BoolValue        ValueType = 2
	IntValue         ValueType = 3

	Required      OptionType = 0
	Optional      OptionType = 1
	Unimplemented OptionType = -1

	showCmdOptionHelpDesc = "[help=true]Show this message"
)

func (ov OptionValue) String() (string, bool) {
	s, ok := ov.value.(string)
	return s, ok
}

func (ov OptionValue) Strings() ([]string, bool) {
	ss, ok := ov.value.([]string)
	return ss, ok
}

func (ov OptionValue) Bool() (bool, bool) {
	b, ok := ov.value.(bool)
	return b, ok
}

func (ov OptionValue) Int() (int, bool) {
	i, ok := ov.value.(int)
	return i, ok
}

func NewShowCmdOption(name string, desc string, valType ValueType) ShowCmdOption {
	return ShowCmdOption{
		optName:     name,
		optType:     Optional,
		description: desc,
		valueType:   valType,
	}
}

func RequiredOption(option ShowCmdOption) ShowCmdOption {
	option.optType = Required
	return option
}

func UnimplementedOption(option ShowCmdOption) ShowCmdOption {
	option.optType = Unimplemented
	return option
}
