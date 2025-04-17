package client

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"
	"time"

	"gopkg.in/yaml.v2"

	"github.com/Workiva/go-datastructures/queue"
	linuxproc "github.com/c9s/goprocinfo/linux"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	spb "github.com/sonic-net/sonic-gnmi/proto"
)

// Non db client is to Handle
// <1> data not in SONiC redis db

const (
	statsRingCap uint64 = 3000 // capacity of statsRing.

	// SonicVersionFilePath is the path of build version YML file.
	SonicVersionFilePath = "/etc/sonic/sonic_version.yml"
)

type dataGetFunc func() ([]byte, error)

type path2DataFunc struct {
	path    []string
	getFunc dataGetFunc
}

type statsRing struct {
	writeIdx uint64 // slot index to write next
	buff     []*linuxproc.Stat
	mu       sync.RWMutex // Mutex for data protection
}

// SonicVersionInfo is a data model to serialize '/etc/sonic/sonic_version.yml'
type SonicVersionInfo struct {
	BuildVersion string `yaml:"build_version" json:"build_version"`
	Error        string `json:"error"` // Applicable only when there is a failure while reading the file.
}

// sonicVersionYmlStash caches the content of '/etc/sonic/sonic_version.yml'
// Assumed that the content of the file doesn't change during the lifetime of telemetry service.
type sonicVersionYmlStash struct {
	once        sync.Once // sync object to make sure file is loaded only once.
	versionInfo SonicVersionInfo
}

// InvalidateVersionFileStash invalidates the cache that keeps version file content.
func InvalidateVersionFileStash() {
	versionFileStash = sonicVersionYmlStash{}
}

var (
	clientTrie *Trie
	statsR     statsRing

	versionFileStash sonicVersionYmlStash

	// ImplIoutilReadFile points to the implementation of ioutil.ReadFile. Should be overridden by UTs only.
	ImplIoutilReadFile func(string) ([]byte, error) = ioutil.ReadFile

	// path2DataFuncTbl is used to populate trie tree which is responsible
	// for getting data at the path specified
	path2DataFuncTbl = []path2DataFunc{
		{ // Get cpu utilization
			path:    []string{"OTHERS", "platform", "cpu"},
			getFunc: dataGetFunc(getCpuUtil),
		},
		{ // Get host uptime
			path:    []string{"OTHERS", "proc", "uptime"},
			getFunc: dataGetFunc(getSysUptime),
		},
		{ // Get proc meminfo
			path:    []string{"OTHERS", "proc", "meminfo"},
			getFunc: dataGetFunc(getProcMeminfo),
		},
		{ // Get proc diskstats
			path:    []string{"OTHERS", "proc", "diskstats"},
			getFunc: dataGetFunc(getProcDiskstats),
		},
		{ // Get proc loadavg
			path:    []string{"OTHERS", "proc", "loadavg"},
			getFunc: dataGetFunc(getProcLoadavg),
		},
		{ // Get proc vmstat
			path:    []string{"OTHERS", "proc", "vmstat"},
			getFunc: dataGetFunc(getProcVmstat),
		},
		{ // Get proc stat
			path:    []string{"OTHERS", "proc", "stat"},
			getFunc: dataGetFunc(getProcStat),
		},
		{ // OS build version
			path:    []string{"OTHERS", "osversion", "build"},
			getFunc: dataGetFunc(getBuildVersion),
		},
	}
)

func (t *Trie) clientTriePopulate() {
	for _, pt := range path2DataFuncTbl {
		n := t.Add(pt.path, pt.getFunc)
		if n.meta.(dataGetFunc) == nil {
			log.V(1).Infof("Failed to add trie node for %v with %v", pt.path, pt.getFunc)
		} else {
			log.V(2).Infof("Add trie node for %v with %v", pt.path, pt.getFunc)
		}

	}
}

type cpuStat struct {
	CpuUsageAll cpuUtil   `json:"cpu_all"`
	CpuUsage    []cpuUtil `json:"cpus"`
}

// Cpu utilization rate
type cpuUtil struct {
	Id            string `json:"id"`
	CpuUtil_100ms uint64 `json:"100ms"`
	CpuUtil_1s    uint64 `json:"1s"`
	CpuUtil_5s    uint64 `json:"5s"`
	CpuUtil_1min  uint64 `json:"1min"`
	CpuUtil_5min  uint64 `json:"5min"`
}

func getCpuUtilPercents(cur, last *linuxproc.CPUStat) uint64 {
	curTotal := (cur.User + cur.Nice + cur.System + cur.Idle + cur.IOWait + cur.IRQ + cur.SoftIRQ + cur.Steal)
	lastTotal := (last.User + last.Nice + last.System + last.Idle + last.IOWait + last.IRQ + last.SoftIRQ + last.Steal)
	idleTicks := cur.Idle - last.Idle
	totalTicks := curTotal - lastTotal
	if totalTicks == 0 { // No change in CPU Utilization
		return 0
	}
	return 100 * (totalTicks - idleTicks) / totalTicks
}

func getCpuUtilStat() *cpuStat {

	stat := cpuStat{}
	statsR.mu.RLock()
	defer statsR.mu.RUnlock()

	current := (statsR.writeIdx + statsRingCap - 1) % statsRingCap
	// Get cpu utilization rate within last 100ms
	last := (statsR.writeIdx + statsRingCap - 2) % statsRingCap
	if statsR.buff[last] == nil {
		return &stat
	}

	curCpuStat := statsR.buff[current].CPUStatAll
	lastCpuStat := statsR.buff[last].CPUStatAll

	CpuUtil_100ms := getCpuUtilPercents(&curCpuStat, &lastCpuStat)
	stat.CpuUsageAll.Id = curCpuStat.Id
	stat.CpuUsageAll.CpuUtil_100ms = CpuUtil_100ms
	for i, cStat := range statsR.buff[last].CPUStats {
		CpuUtil_100ms = getCpuUtilPercents(&statsR.buff[current].CPUStats[i], &cStat)
		stat.CpuUsage = append(stat.CpuUsage, cpuUtil{Id: cStat.Id, CpuUtil_100ms: CpuUtil_100ms})
	}

	// Get cpu utilization rate within last 1s (10*100ms)
	last = (statsR.writeIdx + statsRingCap - 10) % statsRingCap
	if statsR.buff[last] == nil {
		return &stat
	}
	lastCpuStat = statsR.buff[last].CPUStatAll
	CpuUtil_1s := getCpuUtilPercents(&curCpuStat, &lastCpuStat)
	stat.CpuUsageAll.CpuUtil_1s = CpuUtil_1s
	for i, cStat := range statsR.buff[last].CPUStats {
		CpuUtil_1s = getCpuUtilPercents(&statsR.buff[current].CPUStats[i], &cStat)
		stat.CpuUsage[i].CpuUtil_1s = CpuUtil_1s
	}

	// Get cpu utilization rate within last 5s (50*100ms)
	last = (statsR.writeIdx + statsRingCap - 50) % statsRingCap
	if statsR.buff[last] == nil {
		return &stat
	}
	lastCpuStat = statsR.buff[last].CPUStatAll
	CpuUtil_5s := getCpuUtilPercents(&curCpuStat, &lastCpuStat)
	stat.CpuUsageAll.CpuUtil_5s = CpuUtil_5s
	for i, cStat := range statsR.buff[last].CPUStats {
		CpuUtil_5s = getCpuUtilPercents(&statsR.buff[current].CPUStats[i], &cStat)
		stat.CpuUsage[i].CpuUtil_5s = CpuUtil_5s
	}

	// Get cpu utilization rate within last 1m (600*100ms)
	last = (statsR.writeIdx + statsRingCap - 600) % statsRingCap
	if statsR.buff[last] == nil {
		return &stat
	}
	lastCpuStat = statsR.buff[last].CPUStatAll
	CpuUtil_1min := getCpuUtilPercents(&curCpuStat, &lastCpuStat)
	stat.CpuUsageAll.CpuUtil_1min = CpuUtil_1min
	for i, cStat := range statsR.buff[last].CPUStats {
		CpuUtil_1min = getCpuUtilPercents(&statsR.buff[current].CPUStats[i], &cStat)
		stat.CpuUsage[i].CpuUtil_1min = CpuUtil_1min
	}

	// Get cpu utilization rate within last 5m (5*600*100ms)
	last = (statsR.writeIdx + statsRingCap - 30000) % statsRingCap
	if statsR.buff[last] == nil {
		return &stat
	}
	lastCpuStat = statsR.buff[last].CPUStatAll
	CpuUtil_5min := getCpuUtilPercents(&curCpuStat, &lastCpuStat)
	stat.CpuUsageAll.CpuUtil_5min = CpuUtil_5min
	for i, cStat := range statsR.buff[last].CPUStats {
		CpuUtil_5min = getCpuUtilPercents(&statsR.buff[current].CPUStats[i], &cStat)
		stat.CpuUsage[i].CpuUtil_5min = CpuUtil_5min
	}
	return &stat
}

func getCpuUtil() ([]byte, error) {
	cpuStat := getCpuUtilStat()
	log.V(4).Infof("getCpuUtil, cpuStat %v", cpuStat)
	b, err := json.Marshal(cpuStat)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getCpuUtil, output %v", string(b))
	return b, nil
}

func getProcMeminfo() ([]byte, error) {
	memInfo, _ := linuxproc.ReadMemInfo("/proc/meminfo")
	b, err := json.Marshal(memInfo)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getProcMeminfo, output %v", string(b))
	return b, nil
}

func getProcDiskstats() ([]byte, error) {
	diskStats, _ := linuxproc.ReadDiskStats("/proc/diskstats")
	b, err := json.Marshal(diskStats)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getProcDiskstats, output %v", string(b))
	return b, nil
}

func getProcLoadavg() ([]byte, error) {
	loadAvg, _ := linuxproc.ReadLoadAvg("/proc/loadavg")
	b, err := json.Marshal(loadAvg)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getProcLoadavg, output %v", string(b))
	return b, nil
}

func getProcVmstat() ([]byte, error) {
	vmStat, _ := linuxproc.ReadVMStat("/proc/vmstat")
	b, err := json.Marshal(vmStat)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getProcVmstat, output %v", string(b))
	return b, nil
}

func getProcStat() ([]byte, error) {
	stat, _ := linuxproc.ReadStat("/proc/stat")
	b, err := json.Marshal(stat)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getProcStat, output %v", string(b))
	return b, nil
}

func getSysUptime() ([]byte, error) {
	uptime, _ := linuxproc.ReadUptime("/proc/uptime")
	b, err := json.Marshal(uptime)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}

	log.V(4).Infof("getSysUptime, output %v", string(b))
	return b, nil
}

func getBuildVersion() ([]byte, error) {

	// Load and parse the content of version file
	versionFileStash.once.Do(func() {
		versionFileStash.versionInfo.BuildVersion = "sonic.NA"
		versionFileStash.versionInfo.Error = "" // empty string means no error.

		fileContent, err := ImplIoutilReadFile(SonicVersionFilePath)
		if err != nil {
			log.Errorf("Failed to read '%v', %v", SonicVersionFilePath, err)
			versionFileStash.versionInfo.Error = err.Error()
			return
		}

		err = yaml.Unmarshal(fileContent, &versionFileStash.versionInfo)
		if err != nil {
			log.Errorf("Failed to parse '%v', %v", SonicVersionFilePath, err)
			versionFileStash.versionInfo.Error = err.Error()
			return
		}

		// Prepend 'sonic.' to the build version string.
		if versionFileStash.versionInfo.BuildVersion != "sonic.NA" {
			versionFileStash.versionInfo.BuildVersion = "SONiC." + versionFileStash.versionInfo.BuildVersion
		}
	})

	b, err := json.Marshal(versionFileStash.versionInfo)
	if err != nil {
		log.V(2).Infof("%v", err)
		return b, err
	}
	log.V(4).Infof("getBuildVersion, output %v", string(b))
	return b, nil
}

func WriteStatsToBuffer(stat *linuxproc.Stat) {
	statsR.mu.Lock()
	statsR.buff[statsR.writeIdx] = stat
	statsR.writeIdx++
	statsR.writeIdx %= statsRingCap
	statsR.mu.Unlock()
}

func PollStats() {
	for {
		stat, err := linuxproc.ReadStat("/proc/stat")
		if err != nil {
			log.V(2).Infof("stat read fail")
			continue
		}

		WriteStatsToBuffer(stat)
		time.Sleep(time.Millisecond * 100)
	}

}

func init() {
	clientTrie = NewTrie()
	clientTrie.clientTriePopulate()
	statsR.buff = make([]*linuxproc.Stat, statsRingCap)
	go PollStats()
}

type NonDbClient struct {
	prefix      *gnmipb.Path
	path2Getter map[*gnmipb.Path]dataGetFunc

	q       *queue.PriorityQueue
	channel chan struct{}

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for DbClient

	sendMsg int64
	recvMsg int64
	errors  int64
}

func lookupGetFunc(prefix, path *gnmipb.Path) (dataGetFunc, error) {
	stringSlice := []string{prefix.GetTarget()}
	fullPath := gnmiFullPath(prefix, path)

	elems := fullPath.GetElem()
	if elems != nil {
		for i, elem := range elems {
			// TODO: Usage of key field
			log.V(6).Infof("index %d elem : %#v %#v", i, elem.GetName(), elem.GetKey())
			stringSlice = append(stringSlice, elem.GetName())
		}
	}
	n, ok := clientTrie.Find(stringSlice)
	if ok {
		getter := n.meta.(dataGetFunc)
		return getter, nil
	}
	return nil, fmt.Errorf("%v not found in clientTrie tree", stringSlice)
}

func NewNonDbClient(paths []*gnmipb.Path, prefix *gnmipb.Path) (Client, error) {
	var ndc NonDbClient
	ndc.path2Getter = make(map[*gnmipb.Path]dataGetFunc)
	ndc.prefix = prefix
	for _, path := range paths {
		getter, err := lookupGetFunc(prefix, path)
		if err != nil {
			return nil, err
		}
		ndc.path2Getter[path] = getter
	}

	return &ndc, nil
}

// String returns the target the client is querying.
func (c *NonDbClient) String() string {
	// TODO: print gnmiPaths of this NonDbClient
	return fmt.Sprintf("NonDbClient Prefix %v  sendMsg %v, recvMsg %v",
		c.prefix.GetTarget(), c.sendMsg, c.recvMsg)
}

// StreamRun implements stream subscription for non-DB queries. It supports SAMPLE mode only.
func (c *NonDbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	c.q = q

	validatedSubs := make(map[*gnmipb.Subscription]time.Duration)

	// Validate all subs
	for _, sub := range subscribe.GetSubscription() {
		subMode := sub.GetMode()
		if subMode != gnmipb.SubscriptionMode_SAMPLE {
			putFatalMsg(c.q, fmt.Sprintf("Unsupported subscription mode: %v.", subMode))
			return
		}

		interval, err := validateSampleInterval(sub)
		if err != nil {
			putFatalMsg(c.q, err.Error())
			return
		}

		gnmiPath := sub.GetPath()
		_, ok := c.path2Getter[gnmiPath]
		if !ok {
			log.V(3).Infof("Cannot find getter for the path: %v", gnmiPath)
			continue
		}

		validatedSubs[sub] = interval
	}

	if len(validatedSubs) == 0 {
		log.V(3).Infof("No valid sub for stream subscription.")
		return
	}

	for sub := range validatedSubs {
		gnmiPath := sub.GetPath()
		getter, _ := c.path2Getter[gnmiPath]
		runGetterAndSend(c, gnmiPath, getter)
	}

	c.q.Put(Value{
		&spb.Value{
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: true,
		},
	})

	// Start a GO routine for each sub as they might have different intervals
	for sub, interval := range validatedSubs {
		go streamSample(c, stop, sub, interval)
	}

	log.V(1).Infof("Started non-db sampling routines for %s ", c)
	<-stop
	log.V(1).Infof("Stopping NonDbClient.StreamRun routine for Client %s ", c)
}

// streamSample implements the sampling loop for a streaming subscription.
func streamSample(c *NonDbClient, stop chan struct{}, sub *gnmipb.Subscription, interval time.Duration) {
	log.V(1).Infof("Starting sampling routine sub: '%s' client: '%s'", sub, c)

	gnmiPath := sub.GetPath()
	getter, _ := c.path2Getter[gnmiPath] // this is already a validated sub, getter should be there.

	for {
		select {
		case <-stop:
			log.V(1).Infof("Stopping NonDbClient.streamSample routine for sub '%s'", sub)
			return
		case <-time.After(interval):
			runGetterAndSend(c, gnmiPath, getter)
		}
	}
}

// runGetterAndSend runs a given getter method and puts the result to client queue.
func runGetterAndSend(c *NonDbClient, gnmiPath *gnmipb.Path, getter dataGetFunc) error {
	v, err := getter()
	if err != nil {
		log.V(3).Infof("runGetterAndSend getter error %v, %v", gnmiPath, err)
	}

	spbv := &spb.Value{
		Prefix:       c.prefix,
		Path:         gnmiPath,
		Timestamp:    time.Now().UnixNano(),
		SyncResponse: false,
		Val: &gnmipb.TypedValue{
			Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: v,
			}},
	}

	err = c.q.Put(Value{spbv})
	if err != nil {
		log.V(3).Infof("Failed to put for %v, %v", gnmiPath, err)
	} else {
		log.V(6).Infof("Added spbv #%v", spbv)
	}
	return err
}

func (c *NonDbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = poll

	for {
		_, more := <-c.channel
		if !more {
			log.V(1).Infof("%v poll channel closed, exiting pollDb routine", c)
			return
		}
		t1 := time.Now()
		for gnmiPath, getter := range c.path2Getter {
			runGetterAndSend(c, gnmiPath, getter)
		}

		c.q.Put(Value{
			&spb.Value{
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: true,
			},
		})
		log.V(4).Infof("Sync done, poll time taken: %v ms", int64(time.Since(t1)/time.Millisecond))
	}
}

func (c *NonDbClient) AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *NonDbClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}
func (c *NonDbClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	// wait sync for Get, not used for now
	c.w = w

	var values []*spb.Value
	ts := time.Now()
	for gnmiPath, getter := range c.path2Getter {
		v, err := getter()
		if err != nil {
			log.V(3).Infof("PollRun getter error %v for %v", err, v)
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

// TODO: Log data related to this session
func (c *NonDbClient) Close() error {
	return nil
}

func (c *NonDbClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return nil
}
func (c *NonDbClient) Capabilities() []gnmipb.ModelData {
	return nil
}
func (c *NonDbClient) SentOne(val *Value) {
}

func (c *NonDbClient) FailedSend() {
}
