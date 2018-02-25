package client

import (
	"encoding/json"
	"fmt"
	linuxproc "github.com/c9s/goprocinfo/linux"
	log "github.com/golang/glog"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/workiva/go-datastructures/queue"
	"sync"
	"time"
)

// Non db client is to Handle
// <1> data not in SONiC redis db

const (
	statsRingCap uint64 = 3000 // capacity of statsRing.
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

var (
	clientTrie *Trie
	statsR     statsRing

	// path2DataFuncTbl is used to populate trie tree which is reponsible
	// for getting data at the path specified
	path2DataFuncTbl = []path2DataFunc{
		{ // Get cpu utilizaation
			path:    []string{"OTHERS", "platform", "cpu"},
			getFunc: dataGetFunc(getCpuUtil),
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
	curTotal := (cur.User + cur.Nice + cur.System + cur.Idle + cur.IOWait + cur.IRQ + cur.SoftIRQ + cur.Steal + cur.Guest + cur.GuestNice)
	lastTotal := (last.User + last.Nice + last.System + last.Idle + last.IOWait + last.IRQ + last.SoftIRQ + last.Steal + last.Guest + last.GuestNice)
	idleTicks := cur.Idle - last.Idle
	totalTicks := curTotal - lastTotal
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

func pollStats() {
	for {
		stat, err := linuxproc.ReadStat("/proc/stat")
		if err != nil {
			log.V(2).Infof("stat read fail")
			continue
		}

		statsR.mu.Lock()

		statsR.buff[statsR.writeIdx] = stat
		statsR.writeIdx++
		statsR.writeIdx %= statsRingCap
		statsR.mu.Unlock()
		time.Sleep(time.Millisecond * 100)
	}

}

func init() {
	clientTrie = NewTrie()
	clientTrie.clientTriePopulate()
	statsR.buff = make([]*linuxproc.Stat, statsRingCap)
	go pollStats()
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

// To be implemented
func (c *NonDbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup) {
	return
}

func (c *NonDbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup) {
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
			v, err := getter()
			if err != nil {
				log.V(3).Infof("PollRun getter error %v for %v", err, v)
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

			c.q.Put(Value{spbv})
			log.V(6).Infof("Added spbv #%v", spbv)
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

func (c *NonDbClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
	// wait sync for Get, not used for now
	c.w = w

	var values []*spb.Value
	ts := time.Now()
	for gnmiPath, getter := range c.path2Getter {
		v, err := getter()
		if err != nil {
			log.V(3).Infof("PollRun getter error %v for %v", err, v)
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
