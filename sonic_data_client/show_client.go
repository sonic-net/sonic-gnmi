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

type DataGetter func() ([]byte, error)

var showTrie *Trie = NewTrie()

func RegisterCliPath(path []string, getter DataGetter) {
	n := showTrie.Add(path, getter)
	if n.meta.(DataGetter) == nil {
		log.V(1).Infof("Failed to add trie node for %v with %v", path, getter)
	} else {
		log.V(2).Infof("Add trie node for %v with %v", path, getter)
	}
}

type ShowClient struct {
	prefix      *gnmipb.Path
	path2Getter map[*gnmipb.Path]DataGetter

	q       *queue.PriorityQueue
	channel chan struct{}

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for DbClient

	sendMsg int64
	recvMsg int64
	errors  int64
}

func lookupDataGetter(prefix, path *gnmipb.Path) (DataGetter, error) {
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
		getter := n.meta.(DataGetter)
		return getter, nil
	}
	return nil, fmt.Errorf("%v not found in clientTrie tree", stringSlice)
}

func NewShowClient(paths []*gnmipb.Path, prefix *gnmipb.Path) (Client, error) {
	var showClient ShowClient
	showClient.path2Getter = make(map[*gnmipb.Path]DataGetter)
	showClient.prefix = prefix
	for _, path := range paths {
		getter, err := lookupDataGetter(prefix, path)
		if err != nil {
			return nil, err
		}
		showClient.path2Getter[path] = getter
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
	for gnmiPath, getter := range c.path2Getter {
		v, err := getter()
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

func GetDataFromFile(fileName string) ([]byte, error) {
	fileContent, err := ImplIoutilReadFile(fileName)
	if err != nil {
		log.Errorf("Failed to read'%v', %v", fileName, err)
		return nil, err
	}
	log.V(4).Infof("getDataFromFile, output: %v", string(fileContent))
	return fileContent, nil
}

func GetDataFromTablePaths(tblPaths []tablePath) ([]byte, error) {
	msi := make(map[string]interface{})
	for _, tblPath := range tblPaths {
		err := TableData2Msi(&tblPath, false, nil, &msi)
		if err != nil {
			return nil, err
		}
	}
	return Msi2Bytes(msi)
}

func CreateTablePathsFromQueries(queries [][]string) ([]tablePath, error) {
	var allPaths []tablePath

	// Create and validate gnmi path then create table path
	for _, q := range queries {
		if len(q) < 2 || len(q) > 3 {
			return nil, fmt.Errorf("invalid query %v: must be [DB, table] or [DB, table, key]", q)
		}

		dbTarget := q[0]
		table := q[1]
		var key string
		if len(q) == 3 {
			key = q[2]
		}

		// Build a gNMI path for validation:
		//   prefix = { Target: dbTarget }
		//   path   = { Elem: [ {Name:table}, {Name:key} ] }

		prefix := &gnmipb.Path{Target: dbTarget}
		elems := []*gnmipb.PathElem{{Name: table}}
		if key != "" {
			elems = append(elems, &gnmipb.PathElem{Name: key})
		}
		path := &gnmipb.Path{Elem: elems}

		m := make(map[*gnmipb.Path][]tablePath)
		if err := populateDbtablePath(prefix, path, &m); err != nil {
			return nil, fmt.Errorf("query %v failed: %w", q, err)
		}

		tablePaths := m[path]
		allPaths = append(allPaths, tablePaths...)
	}
	return allPaths, nil
}

func Msi2Bytes(msi map[string]interface{}) ([]byte, error) {
	jv, err := emitJSON(&msi)
	if err != nil {
		log.V(2).Infof("emtJSON err %s for %v", err, msi)
		return nil, fmt.Errorf("emitJSON err %s for %v", err, msi)
	}
	if jv == nil {
		return nil, fmt.Errorf("emitJSON failed to grab json value of map")
	}
	return jv, nil
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
