package client

import (
	"encoding/json"
    "fmt"
	"sync"
    "strconv"
	"time"

	spb "github.com/Azure/sonic-telemetry/proto"
	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

type EventClient struct {

	prefix      *gnmipb.Path
    path        *gnmipb.Path

    q       *queue.PriorityQueue
	channel chan struct{}

    w      *sync.WaitGroup // wait for all sub go routines to finish
}


func NewEventClient(paths []*gnmipb.Path, prefix *gnmipb.Path) (Client, error) {
    var evtc EventClient
    evtc.prefix = prefix
    for _, path := range paths {
        // Only one path is expected. Take the last if many
        evtc.path = path
    }
    log.Errorf("NewEventClient constructed");

    return &evtc, nil
}

// String returns the target the client is querying.
func (c *EventClient) String() string {
	return fmt.Sprintf("EventClient Prefix %v", c.prefix.GetTarget())
}


func get_events(c *EventClient, updateChannel chan map[string]interface{}) {
    
    for i := 0; i<10; i++  {
        newMsi := make(map[string]interface{})
        data := make(map[string]interface{})

        data["foo"] = "bar"
        data["hello"] = "world"

        newMsi["event_" + strconv.Itoa(i)] = data

        updateChannel <- newMsi

        time.After(time.Second)
        log.Errorf("get_events i=%d", i);
    }
    return
}


func send_event(c *EventClient, val *map[string]interface{}) error {
    log.Errorf("send_event calling json.Marshal");
    j, err := json.Marshal(*val)
    if err != nil {
        log.Errorf("emitJSON Failed")
        log.Errorf("emitJSON err %s for  %v", err, *val)
        return err
    }

    log.Errorf("send_event calling gnmipb.TypedValue");
    tv := &gnmipb.TypedValue {
        Value: &gnmipb.TypedValue_JsonIetfVal{
            JsonIetfVal: j,
        }}

    spbv := &spb.Value{
        Prefix:    c.prefix,
        Path:    c.path,
        Timestamp: time.Now().UnixNano(),
        Val:  tv,
    }

    log.Errorf("Sending spbv");
    log.Errorf("spbv: %v", *spbv);
    if err = c.q.Put(Value{spbv}); err != nil {
        log.Errorf("Queue error:  %v", err)
        return err
    }
    log.Errorf("send_event done")
    return nil
}

func (c *EventClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    data := make(map[string]interface{})
    data["heart"] = "beat"

	c.w = w
    defer c.w.Done()

    c.q = q
    c.channel = stop

    updateChannel := make(chan map[string]interface{})
    go get_events(c, updateChannel)

    for {
        select {
        case nextEvent := <-updateChannel:
            log.Errorf("update received: %v", nextEvent)
            if err := send_event(c, &nextEvent); err != nil {
                return
            }

        case <-IntervalTicker(time.Second * 2):
            log.Errorf("Ticker received")
            if err := send_event(c, &data); err != nil {
                return
            }
        case <-c.channel:
            log.Errorf("Channel closed by client")
            return
        }
    }
    log.Errorf("Event exiting streamrun")
}


// TODO: Log data related to this session

func (c *EventClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {
    return nil, nil
}

func (c *EventClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    return
}

func (c *EventClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
    return
}


func (c *EventClient) Close() error {
	return nil
}

func  (c *EventClient) Set(delete []*gnmipb.Path, replace []*gnmipb.Update, update []*gnmipb.Update) error {
	return nil
}
func (c *EventClient) Capabilities() []gnmipb.ModelData {
	return nil
}
