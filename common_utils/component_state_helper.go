package common_utils

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"

	log "github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

const (
	dbName              = "STATE_DB"
	componentStateTable = "COMPONENT_STATE_TABLE"
	alarmStatusTable    = "CHASSIS_INFO|chassis"
	alarmStatusKey      = "alarm_status"
)

// SystemComponent is an enum that represents different components in the system.
type SystemComponent int

const (
	InvalidComponent SystemComponent = iota
	Host
	P4rt
	Orchagent
	Syncd
	Telemetry
	Linkqual
	PlatformMonitor
	Inbandmgr
	SwssCfgmgr
)

// String converts a SystemComponent into a string.
func (component SystemComponent) String() string {
	switch component {
	case Host:
		return "host"
	case P4rt:
		return "p4rt:p4rt"
	case Orchagent:
		return "swss:orchagent"
	case Syncd:
		return "syncd:syncd"
	case Telemetry:
		return "telemetry:telemetry"
	case Linkqual:
		return "linkqual:linkqual"
	case PlatformMonitor:
		return "pmon"
	case Inbandmgr:
		return "inbandmgr:inbandapp"
	case SwssCfgmgr:
		return "swss:cfgmgr"
	default:
		log.V(0).Infof("Invalid SystemComponent")
		return "invalid:invalid"
	}
}

var systemComponentLookupMap = map[string]SystemComponent{
	"host":                Host,
	"p4rt:p4rt":           P4rt,
	"swss:orchagent":      Orchagent,
	"syncd:syncd":         Syncd,
	"telemetry:telemetry": Telemetry,
	"linkqual:linkqual":   Linkqual,
	"pmon":                PlatformMonitor,
	"inbandmgr:inbandapp": Inbandmgr,
	"swss:cfgmgr":         SwssCfgmgr,
}

// StringToSystemComponent converts a string into a SystemComponent.
func StringToSystemComponent(component string) (SystemComponent, error) {
	if c, ok := systemComponentLookupMap[component]; ok {
		return c, nil
	}
	return InvalidComponent, fmt.Errorf("Invalid SystemComponent string %s", component)
}

// AllComponents defines a list of all valid components.
// It is defined as a map for quick lookup. The boolean value is not used.
var AllComponents = map[SystemComponent]bool{
	Host:            true,
	P4rt:            true,
	Orchagent:       true,
	Syncd:           true,
	Telemetry:       true,
	Linkqual:        true,
	PlatformMonitor: true,
	Inbandmgr:       true,
	SwssCfgmgr:      true,
}

// EssentialComponents defines a list of essential components.
// The essential components' states determine the overall system state.
// It is defined as a map for quick lookup. The boolean value is not used.
var EssentialComponents = map[SystemComponent]bool{
	Host:            true,
	P4rt:            true,
	Orchagent:       true,
	Syncd:           true,
	Telemetry:       true,
	PlatformMonitor: true,
	Inbandmgr:       true,
}

// ComponentState is an enum that represents the component state.
type ComponentState int

const (
	ComponentStateInvalid ComponentState = iota
	// Service is running but not yet ready to serve.
	// If no state is ever reported for a component, it is also considered as in kInitializing state.
	ComponentInitializing
	// Service is ready and fully functional.
	ComponentUp
	// Service is running but encountered a minor error. Service is not impacted.
	ComponentMinor
	// Service is running but encountered an uncorrectable error. Service may be impacted.
	ComponentError
	// Service is not running and it will not be restarted.
	ComponentInactive
)

// String converts a ComponentState into a string.
func (state ComponentState) String() string {
	switch state {
	case ComponentInitializing:
		return "INITIALIZING"
	case ComponentUp:
		return "UP"
	case ComponentMinor:
		return "MINOR"
	case ComponentError:
		return "ERROR"
	case ComponentInactive:
		return "INACTIVE"
	default:
		return "INVALID"
	}
}

var componentStateLookupMap = map[string]ComponentState{
	"INITIALIZING": ComponentInitializing,
	"UP":           ComponentUp,
	"MINOR":        ComponentMinor,
	"ERROR":        ComponentError,
	"INACTIVE":     ComponentInactive,
}

// StringToComponentState converts a string into a ComponentState.
func StringToComponentState(state string) (ComponentState, error) {
	if s, ok := componentStateLookupMap[state]; ok {
		return s, nil
	}
	return ComponentStateInvalid, fmt.Errorf("Invalid ComponentState string %s", state)
}

// SystemState is an enum that represents the overall system state.
// The overall system state is determined by the states of the essential components.
type SystemState int

const (
	SystemStateInvalid SystemState = iota
	// When any essential component is in kInitializing state.
	SystemInitializing
	// When all essential components are in the kUp or kMinor state.
	SystemUp
	// When any essential component is in kError or kInactive state.
	SystemCritical
)

// ComponentStateInfo includes the state, reason, and timestamp of a state update.
type ComponentStateInfo struct {
	State            ComponentState
	Reason           string
	TimestampNanosec uint64
}

func getRedisDBClient() (*redis.Client, error) {
	addr, _ := sdcfg.GetDbTcpAddr(dbName, "")
	id, _ := sdcfg.GetDbId(dbName, "")
	rclient := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "", // no password set
		DB:          id,
		DialTimeout: 0,
	})
	if rclient == nil {
		return nil, fmt.Errorf("Cannot create redis client.")
	}
	if _, err := rclient.Ping(context.Background()).Result(); err != nil {
		return nil, err
	}
	return rclient, nil
}

type ComponentStateHelperInterface interface {
	Close()
	ReportComponentState(state ComponentState, reason string) error
	ReportHardwareError(state ComponentState, reason string) error
	StateInfo() ComponentStateInfo
}

// ComponentStateHelper provides utilities for reporting component state.
// NewComponentStateHelper must be called for a new helper.
// All queries will be returned from the cache instead of reading the DB values.
// When multiple processes associate to a single component, they should use the
// SystemStateHelper to receive the correct component state.
type ComponentStateHelper struct {
	componentId string
	essential   bool
	stateInfo   ComponentStateInfo
	luaSha      string
	db          *redis.Client
	mux         sync.Mutex
}

// NewComponentStateHelper returns a new ComponentStateHelper.
func NewComponentStateHelper(component SystemComponent) (*ComponentStateHelper, error) {
	h := new(ComponentStateHelper)
	h.componentId = component.String()
	_, h.essential = EssentialComponents[component]
	// A component is in initializing state if no state has been reported.
	h.stateInfo.State = ComponentInitializing

	// Create redis client.
	var err error
	if h.db, err = getRedisDBClient(); err != nil {
		return nil, err
	}

	// Load the component state helper lua script.
	if h.luaSha, err = h.db.ScriptLoad(context.Background(), componentStateHelperLua).Result(); err != nil {
		h.Close()
		return nil, err
	}

	return h, nil
}

// Close performs cleanup works.
// Close must be called when finished.
func (h *ComponentStateHelper) Close() {
	if h == nil {
		return
	}
	if err := h.db.Close(); err != nil {
		log.V(0).Infof("Fail to close Redis client: %v", err)
	}
}

func (h *ComponentStateHelper) callLuaScript(state ComponentState, reason string, hwErr bool) error {
	if h == nil {
		return fmt.Errorf("Helper is nil.")
	}
	h.mux.Lock()
	defer h.mux.Unlock()

	timestamp := time.Now().UTC()
	timestampStr := timestamp.Format("2006-01-02 15:04:05")
	timestampNanosec := timestamp.UnixNano()
	timespecSec := timestampNanosec / 1000000000
	timespecNanosec := timestampNanosec % 1000000000
	hwErrStr := strconv.FormatBool(hwErr)
	essentialFlagStr := strconv.FormatBool(h.essential)
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	r, err := h.db.EvalSha(context.Background(), h.luaSha, []string{componentStateTable + sep}, h.componentId, state.String(), reason, timestampStr, timespecSec, timespecNanosec, hwErrStr, essentialFlagStr).Result()
	if err != nil {
		return err
	}
	results, ok := r.([]interface{})
	if !ok {
		return fmt.Errorf("Error in getting redis reply.")
	}
	if len(results) != 3 {
		return fmt.Errorf("Error in getting redis reply.")
	}
	updated, ok := results[0].(string)
	if !ok {
		return fmt.Errorf("Error in getting redis reply.")
	}
	oldState, ok := results[1].(string)
	if !ok {
		return fmt.Errorf("Error in getting redis reply.")
	}
	newState, ok := results[2].(string)
	if !ok {
		return fmt.Errorf("Error in getting redis reply.")
	}

	if updated != "true" {
		log.V(0).Infof("Component %s failed to updated state from %s to %v with reason: %s.", h.componentId, oldState, state, reason)
		return fmt.Errorf("Component %s failed to update state from %s to %v with reason: %s.", h.componentId, oldState, state, reason)
	}
	log.V(2).Infof("Component %s successfully updated state from %s to %s with reason: %s.", h.componentId, oldState, newState, reason)
	h.stateInfo.State = state
	h.stateInfo.Reason = reason
	h.stateInfo.TimestampNanosec = uint64(timestampNanosec)

	return nil
}

// ReportComponentState reports a component state update.
func (h *ComponentStateHelper) ReportComponentState(state ComponentState, reason string) error {
	return h.callLuaScript(state, reason, false)
}

// ReportHardwareError reports a component state update with indication of a hardware related error.
// Same as ReportComponentState, but only applies to ComponentMinor and ComponentError states.
func (h *ComponentStateHelper) ReportHardwareError(state ComponentState, reason string) error {
	if state != ComponentMinor && state != ComponentError {
		return fmt.Errorf("Cannot report hardware error for %v state.", state)
	}
	return h.callLuaScript(state, reason, true)
}

// StateInfo returns the self component state information.
// Returns the default value of ComponentStateInfo if the component did not report any state.
func (h *ComponentStateHelper) StateInfo() ComponentStateInfo {
	if h == nil {
		return ComponentStateInfo{}
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.stateInfo
}

type SystemStateHelperInterface interface {
	Close()
	GetSystemState() SystemState
	IsSystemCritical() bool
	GetSystemCriticalReason() string
	AllComponentStates() map[SystemComponent]ComponentStateInfo
}

// SystemStateHelper provides utilities for querying and receiving system state information.
// NewSystemStateHelper must be called for a new SystemStateHelper.
// Close must be called when finished.
type SystemStateHelper struct {
	systemState                 SystemState
	systemCriticalReason        string
	systemCriticalDb            bool
	essentialInactiveComponents map[string]bool
	componentStateInfo          map[SystemComponent]ComponentStateInfo
	pubSubDB                    *redis.Client
	db                          *redis.Client
	mux                         sync.Mutex
	wg                          sync.WaitGroup
	done                        chan bool
	channel                     <-chan *redis.Message
}

// NewSystemStateHelper returns a new SystemStateHelper.
func NewSystemStateHelper() (*SystemStateHelper, error) {
	h := new(SystemStateHelper)
	h.essentialInactiveComponents = map[string]bool{}
	h.componentStateInfo = map[SystemComponent]ComponentStateInfo{}
	h.done = make(chan bool, 1)
	h.systemState = SystemInitializing
	for ec, _ := range EssentialComponents {
		h.componentStateInfo[ec] = ComponentStateInfo{State: ComponentInitializing}
	}

	// Create redis client and subscribe for SERVICE_STATUS_TABLE update.
	var err error
	if h.pubSubDB, err = getRedisDBClient(); err != nil {
		return nil, err
	}
	if h.db, err = getRedisDBClient(); err != nil {
		h.pubSubDB.Close()
		return nil, err
	}

	// Read the current DB and update the cache.
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	if keys, err := h.db.Keys(context.Background(), componentStateTable+sep+"*").Result(); err == nil {
		for _, key := range keys {
			splitedTableKeys := strings.SplitN(key, sep, 2)
			if len(splitedTableKeys) < 2 {
				log.V(0).Infof("Missing table key in %s.", key)
				continue
			}
			h.processComponentStateUpdate(splitedTableKeys[1])
		}
	}
	h.systemCriticalDb = !h.IsSystemCritical()
	if err := h.writeSystemCritical(h.IsSystemCritical()); err != nil {
		log.V(0).Infof("Failed writing state to DB: %s", err.Error())
	}

	// Subscribe for COMPONENT_STATE_TABLE update.
	id, _ := sdcfg.GetDbId(dbName, "")
	channelStr := "__keyspace@" + strconv.Itoa(id) + "__:" + componentStateTable + sep + "*"
	sub := h.pubSubDB.PSubscribe(context.Background(), channelStr)
	if _, err = sub.Receive(context.Background()); err != nil {
		h.pubSubDB.Close()
		h.db.Close()
		return nil, err
	}
	h.channel = sub.Channel()

	// Start a goroutine to listen to COMPONENT_STATE_TABLE update.
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case msg := <-h.channel:
				if msg == nil {
					continue
				}
				splitedKeys := strings.SplitN(msg.Channel, ":", 2)
				if len(splitedKeys) < 2 {
					log.V(0).Infof("Missing key in Redis namespace notification channel %s.", msg.Channel)
					continue
				}
				splitedTableKeys := strings.SplitN(splitedKeys[1], sep, 2)
				if len(splitedTableKeys) < 2 {
					log.V(0).Infof("Missing table key in %s.", splitedKeys[1])
					continue
				}
				h.processComponentStateUpdate(splitedTableKeys[1])
				if err := h.writeSystemCritical(h.IsSystemCritical()); err != nil {
					log.V(0).Infof("Failed writing state to DB: %s", err.Error())
				}
			case <-h.done:
				h.cleanup()
				return
			}
		}
	}()

	return h, nil
}

func (h *SystemStateHelper) writeSystemCritical(status bool) error {
	if status == h.systemCriticalDb {
		return nil
	}
	s := "false"
	if status {
		s = "true"
	}
	err := h.db.HSet(context.Background(), alarmStatusTable, alarmStatusKey, s).Err()
	if err == nil {
		h.systemCriticalDb = status
	}
	return err
}

// Close stops the SystemStateHelper.
// Close must be called when finished.
func (h *SystemStateHelper) Close() {
	if h == nil {
		return
	}
	h.done <- true
	h.wg.Wait()
}

// cleanup performs cleanup work.
func (h *SystemStateHelper) cleanup() {
	if h == nil {
		return
	}
	if err := h.pubSubDB.Close(); err != nil {
		log.V(0).Infof("Fail to close Redis client: %v", err)
	}
	if err := h.db.Close(); err != nil {
		log.V(0).Infof("Fail to close Redis client: %v", err)
	}
}

// GetSystemState returns the overall system state.
func (h *SystemStateHelper) GetSystemState() SystemState {
	if h == nil {
		return SystemStateInvalid
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.systemState
}

// IsSystemCritical returns true if the system state is SystemCritical.
func (h *SystemStateHelper) IsSystemCritical() bool {
	if h == nil {
		return false
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.systemState == SystemCritical
}

// GetSystemCriticalReason returns a description of why the system state is SystemCritical.
// Returns empty string if the system state is not SystemCritical.
// The description is a concatenation of each ComponentError or ComponentInactive essential component's description,
// with space as separator. Each component description has the following format (without quotes):
// "<component_id> in state <state_str> with reason: <reason>."
func (h *SystemStateHelper) GetSystemCriticalReason() string {
	if h == nil {
		return ""
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.systemCriticalReason
}

// AllComponentStates returns state information of all components.
// For essential components, if they didn't report any state, the Initializing state will be returned.
// For non-essential components, if they didn't report any state, no state information will be returned for them.
func (h *SystemStateHelper) AllComponentStates() map[SystemComponent]ComponentStateInfo {
	if h == nil {
		return nil
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	return h.componentStateInfo
}

func (h *SystemStateHelper) processComponentStateUpdate(componentStr string) {
	if h == nil {
		return
	}
	h.mux.Lock()
	defer h.mux.Unlock()
	sep, _ := sdcfg.GetDbSeparator(dbName, "")
	stateInfo, err := h.db.HGetAll(context.Background(), componentStateTable+sep+componentStr).Result()
	if err != nil {
		return
	}
	si, e, err := parseComponentStateInfo(stateInfo)
	if err != nil {
		return
	}
	if e && si.State == ComponentInactive {
		h.essentialInactiveComponents[componentStr] = true
	}
	component, err := StringToSystemComponent(componentStr)
	if err == nil {
		h.componentStateInfo[component] = *si
	}
	if len(h.essentialInactiveComponents) != 0 {
		h.systemState = SystemCritical
		h.systemCriticalReason = ""
		for c, _ := range h.essentialInactiveComponents {
			if len(h.systemCriticalReason) != 0 {
				h.systemCriticalReason += ", "
			}
			h.systemCriticalReason += c
		}
		h.systemCriticalReason = "Container monitor reports INACTIVE for components: " + h.systemCriticalReason
		return
	}
	h.systemState = SystemUp
	h.systemCriticalReason = ""
	for ec, _ := range EssentialComponents {
		stateInfo := h.componentStateInfo[ec]
		if stateInfo.State == ComponentError || stateInfo.State == ComponentInactive {
			h.systemState = SystemCritical
			if h.systemCriticalReason != "" {
				h.systemCriticalReason += " "
			}
			h.systemCriticalReason += ec.String() + " in state " + stateInfo.State.String() + " with reason: " + stateInfo.Reason + "."
		} else if stateInfo.State == ComponentInitializing && h.systemState != SystemCritical {
			h.systemState = SystemInitializing
		}
	}
}

func parseComponentStateInfo(stateInfo map[string]string) (*ComponentStateInfo, bool, error) {
	si := new(ComponentStateInfo)
	essential := false
	for key, value := range stateInfo {
		switch key {
		case "state":
			s, err := StringToComponentState(value)
			if err != nil {
				return nil, false, err
			}
			si.State = s
		case "reason":
			si.Reason = value
		case "timestamp-seconds":
			second, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return nil, false, err
			}
			si.TimestampNanosec += second * 1000000000
		case "timestamp-nanoseconds":
			nanosecond, err := strconv.ParseUint(value, 10, 64)
			if err != nil {
				return nil, false, err
			}
			si.TimestampNanosec += nanosecond
		case "essential":
			if value == "true" {
				essential = true
			}
		}
	}
	return si, essential, nil
}
