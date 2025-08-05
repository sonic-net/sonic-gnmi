package client

import (
	"fmt"
	"sync"
	"time"

	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
)

type OptionType int

type ShowCmdOption struct{
	optName     string
	optType     OptionType // 0 means required, 1 means optional, -1 means unimplemented, all other values means invalid argument
	description string // will be used in help output
}

type DataGetter func(prefix, path *gnmipb.Path) ([]byte, error)

type TablePath = tablePath

type ShowPathConfig struct {
	dataGetter  DataGetter
	options     map[string]ShowCmdOption
	description map[string]map[string]string
}

const (
	Required OptionType      = 0
	Optional OptionType      = 1
	Unimplemented OptionType = -1

	SHOW_CMD_OPT_GLOBAL_HELP_DESC = "[help]Show this message"
)

var (
	showTrie *Trie = NewTrie()

	SHOW_CMD_OPT_GLOBAL_HELP = ShowCmdOption{ // No need to add this in RegisterCliPathWithOpts call as all paths will support
		optName:     "help",
		optType:     Optional,
		description: SHOW_CMD_OPT_GLOBAL_HELP_DESC,
	}
)

func RegisterCliPath(path []string, getter DataGetter, subcommandDesc map[string]string, options ...ShowCmdOption) {
	pathOptions := constructOptions(options)
	pathDescription := constructDescription(subcommandDesc, pathOptions)
	config := ShowPathConfig{
		dataGetter:  getter,
		options:     pathOptions,
		description: pathDescription,
	}
	n := showTrie.Add(path, config)
	if n.meta.(ShowPathConfig) == nil {
		log.V(1).Infof("Failed to add trie node for %v with %v", path, getter)
	} else {
		log.V(2).Infof("Add trie node for %v with %v", path, getter)
	}
}

type ShowClient struct {
	prefix      *gnmipb.Path
	path2Config map[*gnmipb.Path]ShowPathConfig

	q       *queue.PriorityQueue
	channel chan struct{}

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for DbClient

	sendMsg int64
	recvMsg int64
	errors  int64
}

func lookupPathConfig(prefix, path *gnmipb.Path) (ShowPathConfig, error) {
	stringSlice := []string{prefix.GetTarget()}
	fullPath := gnmiFullPath(prefix, path)

	elems := fullPath.GetElem()
	if elems != nil {
		for i, elem := range elems {
			log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
			stringSlice = append(stringSlice, elem.GetName())
		}
	}
	n, ok := showTrie.Find(stringSlice)
	if ok {
		config := n.meta.(ShowPathConfig)
		return config, nil
	}
	return nil, fmt.Errorf("%v not found in clientTrie tree", stringSlice)
}

func NewShowClient(paths []*gnmipb.Path, prefix *gnmipb.Path) (Client, error) {
	var showClient ShowClient
	showClient.path2Config = make(map[*gnmipb.Path]ShowPathConfig)
	showClient.prefix = prefix
	for _, path := range paths {
		config, err := lookupPathConfig(prefix, path)
		if err != nil {
			return nil, err
		}
		showClient.path2Config[path] = config
	}

	return &showClient, nil
}

// String returns the target the client is querying.
func (c *ShowClient) String() string {
	return fmt.Sprintf("ShowClient Prefix %v  sendMsg %v, recvMsg %v",
		c.prefix.GetTarget(), c.sendMsg, c.recvMsg)
}

func (c *ShowClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	c.w = w

	var values []*spb.Value
	ts := time.Now()
	for gnmiPath, config := range c.path2Config {
		getter := config.dataGetter
		options := config.options
		description := config.description
		// Validate options in path
		passedOptions, err := checkForOption(gnmiPath, options)
		if err != nil {
			return nil, err
		}
		// Return description of path if help is passed
		if passedOptions["help"] {
			return showHelp(prefix, path, description)
		}
		// Validate required and unimplemented options
		err := validateOptions(passedOptions, options)
		if err != nil {
			return err
		}

		v, err := getter(c.prefix, gnmiPath)
		if err != nil {
			log.V(3).Infof("GetData error %v for %v", err, v)
			return nil, err
		}
		values = append(values, &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: ts.UnixNano(),
			Val: &gnmipb.TypedValue{
				Value: &gnmipb.TypedValue_JsonIetfVal{
					JsonIetfVal: v,
				}},
		})
	}
	log.V(6).Infof("Getting #%v", values)
	log.V(4).Infof("Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))
	return values, nil
}

func PopulateTablePaths(prefix, path *gnmipb.Path) ([]TablePath, error) {
	m := make(map[*gnmipb.Path][]tablePath)
	if err := populateDbtablePath(prefix, path, &m); err != nil {
		return nil, err
	}
	return m[path], nil
}

func Msi2Bytes(msi map[string]interface{}) ([]byte, error) {
	jv, err := emitJSON(&msi)
	if err != nil {
		log.V(2).Infof("emitJSON err %s for %v", err, msi)
		return nil, fmt.Errorf("emitJSON err %s for %v", err, msi)
	}
	if jv == nil {
		return nil, fmt.Errorf("emitJSON failed to grab json value of map")
	}
	return jv, nil
}

func showHelp(prefix, path *gnmipb.Path, description map[string]string) ([]*spb.Value, error) {
	helpData, err = json.Marshal(description)
	if err != nil {
		return nil, err
	}

	var values []*spb.Value
	ts := time.Now()
	values = append(values, &spb.Value{
		Prefix:    prefix,
		Path:      path,
		Timestamp: ts.UnixNano(),
		Val: &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: v,
			}},
		})
	return values, nil
}

func validateOptions(passedOptions map[string]bool, options map[string]ShowCmdOption) error {
	// Validate that mandatory options exist and unimplemented options are errored out
	for option, value := range options {
		seen := passedOptions[option]
		if seen {
			if value.optType == Unimplemented {
				return status.Errorf(codes.Unimplemented, "option %v is unimplemented", option)
			}
		} else {
			if value.optType == Required {
				return status.Errorf(codes.InvalidArgument, "option %v is required", option)
			}
		}
	}
	return nil
}

func checkForOption(path *gnmipb.Path, options map[string]ShowCmdOption) (map[string]bool, error) {
	// Validate that path doesn't contain any option that is not registered
	seen := make(map[string]bool)
	for _, elem := range path.GetElem() {
		for key := range elem.GetKey() {
			if _, ok := options[key]; !ok {
				return nil, status.Errorf(codes.InvalidArgument, "option %v for path %v is not a valid option", key, path)
			}
			seen[key] = true
		}
	}
	return seen, nil
}

func constructDescription(subcommandDesc map[string]string, options map[string]ShowCmdOption) map[string]map[string]string {
	description := make(map[string]map[string]string)
	description["options"] = make(map[string]string)
	for _, option := range options {
		description["options"][option.optName] = option.description
	}
	description["subcommands"] = subcommandDesc
	return description
}

func constructOptions(options []ShowCmdOption) map[string]ShowCmdOption {
	pathOptions := make(map[string]ShowCmdOption)
	pathOptions[SHOW_CMD_OPT_GLOBAL_HELP.optName] = SHOW_CMD_OPT_GLOBAL_HELP
	for _, option := range options {
		pathOptions[option.optName] = option
	}
	return pathOptions
}

// Unimplemented
func (c *ShowClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *ShowClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *ShowClient) AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *ShowClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *ShowClient) Close() error {
	return nil
}

func (c *ShowClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return nil
}

func (c *ShowClient) Capabilities() []gnmipb.ModelData {
	return nil
}

func (c *ShowClient) SentOne(val *Value) {
}

func (c *ShowClient) FailedSend() {
}
