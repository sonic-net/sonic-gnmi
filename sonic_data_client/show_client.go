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

var (
	clientTrie *Trie
	path2TblPathTbl = []path2TblPath {
		{
			path: []string{"SHOW_CLI", "show", "reboot-cause"}
			getTblPaths: :
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

func init() {
	clientTrie = NewTrie()
	clientTrie.clientTriePopulate()
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

func (c *NonDbClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *NonDbClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
}

func (c *NonDbClient) AppDBPollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

func (c *NonDbClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	return
}

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
