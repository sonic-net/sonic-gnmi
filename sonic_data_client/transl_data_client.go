//  Package client provides a generic access layer for data available in system
package client

import (
	spb "github.com/Azure/sonic-telemetry/proto"
	transutil "github.com/Azure/sonic-telemetry/transl_utils"
	log "github.com/golang/glog"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/Workiva/go-datastructures/queue"
	"sync"
	"time"
	"fmt"
	"reflect"
	"github.com/Azure/sonic-mgmt-common/translib"
	"bytes"
	"encoding/json"
)

const (
	DELETE  int = 0
	REPLACE int = 1
	UPDATE  int = 2
)

type TranslClient struct {
	prefix *gnmipb.Path
	/* GNMI Path to REST URL Mapping */
	path2URI map[*gnmipb.Path]string
	channel  chan struct{}
	q        *queue.PriorityQueue

	synced sync.WaitGroup  // Control when to send gNMI sync_response
	w      *sync.WaitGroup // wait for all sub go routines to finish
	mu     sync.RWMutex    // Mutex for data protection among routines for transl_client

}

func NewTranslClient(prefix *gnmipb.Path, getpaths []*gnmipb.Path) (Client, error) {
	var client TranslClient
	var err error

	client.prefix = prefix
	if getpaths != nil {
		client.path2URI = make(map[*gnmipb.Path]string)
		/* Populate GNMI path to REST URL map. */
		err = transutil.PopulateClientPaths(prefix, getpaths, &client.path2URI)
	}

	if err != nil {
		return nil, err
	} else {
		return &client, nil
	}
}

func (c *TranslClient) Get(w *sync.WaitGroup) ([]*spb.Value, error) {

	var values []*spb.Value
	ts := time.Now()

	/* Iterate through all GNMI paths. */
	for gnmiPath, URIPath := range c.path2URI {
		/* Fill values for each GNMI path. */
		val, err := transutil.TranslProcessGet(URIPath, nil)

		if err != nil {
			return nil, err
		}

		/* Value of each path is added to spb value structure. */
		values = append(values, &spb.Value{
			Prefix:    c.prefix,
			Path:      gnmiPath,
			Timestamp: ts.UnixNano(),
			Val:       val,
		})
	}

	/* The values structure at the end is returned and then updates in notitications as
	specified in the proto file in the server.go */

	log.V(6).Infof("TranslClient : Getting #%v", values)
	log.V(4).Infof("TranslClient :Get done, total time taken: %v ms", int64(time.Since(ts)/time.Millisecond))

	return values, nil
}

func (c *TranslClient) Set(path *gnmipb.Path, val *gnmipb.TypedValue, flagop int) error {
	var uri string
	var err error

	/* Convert the GNMI Path to URI. */
	transutil.ConvertToURI(c.prefix, path, &uri)

	if flagop == DELETE {
		err = transutil.TranslProcessDelete(uri)
	} else if flagop == REPLACE {
		err = transutil.TranslProcessReplace(uri, val)
	} else if flagop == UPDATE {
		err = transutil.TranslProcessUpdate(uri, val)
	}

	return err
}
func enqueFatalMsgTranslib(c *TranslClient, msg string) {
	c.q.Put(Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     msg,
		},
	})
}
type ticker_info struct{
	t              *time.Ticker
	sub            *gnmipb.Subscription
	heartbeat      bool
}

func (c *TranslClient) StreamRun(q *queue.PriorityQueue, stop chan struct{}, w *sync.WaitGroup, subscribe *gnmipb.SubscriptionList) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = stop


	ticker_map := make(map[int][]*ticker_info)
	var cases []reflect.SelectCase
	cases_map := make(map[int]int)
	var subscribe_mode gnmipb.SubscriptionMode
	stringPaths := make([]string, len(subscribe.Subscription))
	for i,sub := range subscribe.Subscription {
		stringPaths[i] = c.path2URI[sub.Path]
	}
	req := translib.IsSubscribeRequest{Paths:stringPaths}
	subSupport,_ := translib.IsSubscribeSupported(req)
	var onChangeSubsString []string
	var onChangeSubsgNMI []*gnmipb.Path
	onChangeMap := make(map[string]*gnmipb.Path)
	valueCache := make(map[string]string)

	for i,sub := range subscribe.Subscription {
		fmt.Println(sub.Mode, sub.SampleInterval)
		switch sub.Mode {

		case gnmipb.SubscriptionMode_TARGET_DEFINED:

			if subSupport[i].Err == nil && subSupport[i].IsOnChangeSupported {
				if subSupport[i].PreferredType == translib.Sample {
					subscribe_mode = gnmipb.SubscriptionMode_SAMPLE
				} else if subSupport[i].PreferredType == translib.OnChange {
					subscribe_mode = gnmipb.SubscriptionMode_ON_CHANGE
				}
			} else {
				subscribe_mode = gnmipb.SubscriptionMode_SAMPLE
			}

		case gnmipb.SubscriptionMode_ON_CHANGE:
			if subSupport[i].Err == nil && subSupport[i].IsOnChangeSupported {
				if (subSupport[i].MinInterval > 0) {
					subscribe_mode = gnmipb.SubscriptionMode_ON_CHANGE
				}else{
					enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid subscribe path %v", stringPaths[i]))
					return
				}
			} else {
				enqueFatalMsgTranslib(c, fmt.Sprintf("ON_CHANGE Streaming mode invalid for %v", stringPaths[i]))
				return
			}
		case gnmipb.SubscriptionMode_SAMPLE:
			if (subSupport[i].MinInterval > 0) {
				subscribe_mode = gnmipb.SubscriptionMode_SAMPLE
			}else{
				enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid subscribe path %v", stringPaths[i]))
				return
			}
		default:
			log.V(1).Infof("Bad Subscription Mode for client %s ", c)
			enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Subscription Mode %d", sub.Mode))
			return
		}
		fmt.Println("subscribe_mode:", subscribe_mode)
		if subscribe_mode == gnmipb.SubscriptionMode_SAMPLE {
			interval := int(sub.SampleInterval)
			if interval == 0 {
				interval = subSupport[i].MinInterval * int(time.Second)
			} else {
				if interval < (subSupport[i].MinInterval*int(time.Second)) {
					enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Sample Interval %ds, minimum interval is %ds", interval/int(time.Second), subSupport[i].MinInterval))
					return
				}
			}
			//Send initial data now so we can send sync response.
			val, err := transutil.TranslProcessGet(c.path2URI[sub.Path], nil)
			if err != nil {
				return
			}
			spbv := &spb.Value{
				Prefix:       c.prefix,
				Path:         sub.Path,
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: false,
				Val:          val,
			}
			c.q.Put(Value{spbv})
			valueCache[c.path2URI[sub.Path]] = string(val.GetJsonIetfVal())
			addTimer(c, ticker_map, &cases, cases_map, interval, sub, false)
			//Heartbeat intervals are valid for SAMPLE in the case suppress_redundant is specified
			if sub.SuppressRedundant && sub.HeartbeatInterval > 0 {
				if int(sub.HeartbeatInterval) < subSupport[i].MinInterval * int(time.Second) {
					enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Heartbeat Interval %ds, minimum interval is %ds", int(sub.HeartbeatInterval)/int(time.Second), subSupport[i].MinInterval))
					return
				}
				addTimer(c, ticker_map, &cases, cases_map, int(sub.HeartbeatInterval), sub, true)
			}
		} else if subscribe_mode == gnmipb.SubscriptionMode_ON_CHANGE {
			onChangeSubsString = append(onChangeSubsString, c.path2URI[sub.Path])
			onChangeSubsgNMI = append(onChangeSubsgNMI, sub.Path)
			onChangeMap[c.path2URI[sub.Path]] = sub.Path
			if sub.HeartbeatInterval > 0 {
				if int(sub.HeartbeatInterval) < subSupport[i].MinInterval * int(time.Second) {
					enqueFatalMsgTranslib(c, fmt.Sprintf("Invalid Heartbeat Interval %ds, minimum interval is %ds", int(sub.HeartbeatInterval)/int(time.Second), subSupport[i].MinInterval))
					return
				}
				addTimer(c, ticker_map, &cases, cases_map, int(sub.HeartbeatInterval), sub, true)
			}
			
		}
	}
	if len(onChangeSubsString) > 0 {
		c.w.Add(1)
		c.synced.Add(1)
		go TranslSubscribe(onChangeSubsgNMI, onChangeSubsString, onChangeMap, c, subscribe.UpdatesOnly)

	}
	// Wait until all data values corresponding to the path(s) specified
	// in the SubscriptionList has been transmitted at least once
	c.synced.Wait()
	spbs := &spb.Value{
		Timestamp:    time.Now().UnixNano(),
		SyncResponse: true,
	}
	c.q.Put(Value{spbs})
	cases = append(cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(c.channel)})

	for {
		chosen, _, ok := reflect.Select(cases)


		if !ok {
			return
		}

		for _,tick := range ticker_map[cases_map[chosen]] {
			fmt.Printf("tick, heartbeat: %t, path: %s", tick.heartbeat, c.path2URI[tick.sub.Path])
			val, err := transutil.TranslProcessGet(c.path2URI[tick.sub.Path], nil)
			if err != nil {
				return
			}
			spbv := &spb.Value{
				Prefix:       c.prefix,
				Path:         tick.sub.Path,
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: false,
				Val:          val,
			}
			

			if (tick.sub.SuppressRedundant) && (!tick.heartbeat) && (string(val.GetJsonIetfVal()) == valueCache[c.path2URI[tick.sub.Path]]) {
				log.V(6).Infof("Redundant Message Suppressed #%v", string(val.GetJsonIetfVal()))
			} else {
				c.q.Put(Value{spbv})
				valueCache[c.path2URI[tick.sub.Path]] = string(val.GetJsonIetfVal())
				log.V(6).Infof("Added spbv #%v", spbv)
			}
			
			
		}
	}
}

func addTimer(c *TranslClient, ticker_map map[int][]*ticker_info, cases *[]reflect.SelectCase, cases_map map[int]int, interval int, sub *gnmipb.Subscription, heartbeat bool) {
	//Reuse ticker for same sample intervals, otherwise create a new one.
	if ticker_map[interval] == nil {
		ticker_map[interval] = make([]*ticker_info, 1, 1)
		ticker_map[interval][0] = &ticker_info {
			t: time.NewTicker(time.Duration(interval) * time.Nanosecond),
			sub: sub,
			heartbeat: heartbeat,
		}
		cases_map[len(*cases)] = interval
		*cases = append(*cases, reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ticker_map[interval][0].t.C)})
	}else {
		ticker_map[interval] = append(ticker_map[interval], &ticker_info {
			t: ticker_map[interval][0].t,
			sub: sub,
			heartbeat: heartbeat,
		})
	}
	

}

func TranslSubscribe(gnmiPaths []*gnmipb.Path, stringPaths []string, pathMap map[string]*gnmipb.Path, c *TranslClient, updates_only bool) {
	defer c.w.Done()
	q := queue.NewPriorityQueue(1, false)
	var sync_done bool
	req := translib.SubscribeRequest{Paths:stringPaths, Q:q, Stop:c.channel}
	translib.Subscribe(req)
	for {
		items, err := q.Get(1)
		if err != nil {
			log.V(1).Infof("%v", err)
			return
		}
		switch v := items[0].(type) {
		case *translib.SubscribeResponse:

			if v.IsTerminated {
				//DB Connection or other backend error
				enqueFatalMsgTranslib(c, "DB Connection Error")
				close(c.channel)
				return
			}

			var jv []byte
			dst := new(bytes.Buffer)
			json.Compact(dst, v.Payload)
			jv = dst.Bytes()

			/* Fill the values into GNMI data structures . */
			val := &gnmipb.TypedValue{
				Value: &gnmipb.TypedValue_JsonIetfVal{
				JsonIetfVal: jv,
				}}

			spbv := &spb.Value{
				Prefix:       c.prefix,
				Path:         pathMap[v.Path],
				Timestamp:    v.Timestamp,
				SyncResponse: false,
				Val:          val,
			}

			//Don't send initial update with full object if user wants updates only.
			if updates_only && !sync_done {
				log.V(1).Infof("Msg suppressed due to updates_only")
			} else {
				c.q.Put(Value{spbv})
			}

			log.V(6).Infof("Added spbv #%v", spbv)
			
			if v.SyncComplete && !sync_done {
				fmt.Println("SENDING SYNC")
				c.synced.Done()
				sync_done = true
			}
		default:
			log.V(1).Infof("Unknown data type %v for %s in queue", items[0], c)
		}
	}
}



func (c *TranslClient) PollRun(q *queue.PriorityQueue, poll chan struct{}, w *sync.WaitGroup) {
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
		for gnmiPath, URIPath := range c.path2URI {
			val, err := transutil.TranslProcessGet(URIPath, nil)
			if err != nil {
				return
			}

			spbv := &spb.Value{
				Prefix:       c.prefix,
				Path:         gnmiPath,
				Timestamp:    time.Now().UnixNano(),
				SyncResponse: false,
				Val:          val,
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
func (c *TranslClient) OnceRun(q *queue.PriorityQueue, once chan struct{}, w *sync.WaitGroup) {
	c.w = w
	defer c.w.Done()
	c.q = q
	c.channel = once

	
	_, more := <-c.channel
	if !more {
		log.V(1).Infof("%v once channel closed, exiting onceDb routine", c)
		return
	}
	t1 := time.Now()
	for gnmiPath, URIPath := range c.path2URI {
		val, err := transutil.TranslProcessGet(URIPath, nil)
		if err != nil {
			return
		}

		spbv := &spb.Value{
			Prefix:       c.prefix,
			Path:         gnmiPath,
			Timestamp:    time.Now().UnixNano(),
			SyncResponse: false,
			Val:          val,
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
	log.V(4).Infof("Sync done, once time taken: %v ms", int64(time.Since(t1)/time.Millisecond))
	
}

func (c *TranslClient) Capabilities() []gnmipb.ModelData {

	/* Fetch the supported models. */
	supportedModels := transutil.GetModels()
	return supportedModels
}

func (c *TranslClient) Close() error {
	return nil
}
