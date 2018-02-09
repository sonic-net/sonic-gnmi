package telemetry_dialout

import (
	// "encoding/json"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	log "github.com/golang/glog"
	"net"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	// "github.com/golang/protobuf/proto"
	"github.com/go-redis/redis"
	spb "github.com/jipanyang/sonic-telemetry/proto"
	sdc "github.com/jipanyang/sonic-telemetry/sonic_data_client"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	// Unknown is an unknown report and should always be treated as an error.
	Unknown reportType = iota
	// Once will perform a Once report against the agent.
	Once
	// Poll will perform a Periodic report against the agent.
	Periodic
	// Stream will perform a Streaming report against the agent.
	Stream
)

// Type defines the type of report.
type reportType int

// NewType returns a new reportType based on the provided string.
func NewReportType(s string) reportType {
	v, ok := typeConst[s]
	if !ok {
		return Unknown
	}
	return v
}

// String returns the string representation of the reportType.
func (r reportType) String() string {
	return typeString[r]
}

var (
	typeString = map[reportType]string{
		Unknown:  "unknown",
		Once:     "once",
		Periodic: "periodic",
		Stream:   "stream",
	}

	typeConst = map[string]reportType{
		"unknown":  Unknown,
		"once":     Once,
		"periodic": Periodic,
		"stream":   Stream,
	}
	clientCfg *ClientConfig
	// Global mutex for protecting the config data
	configMu sync.Mutex

	// Each Destination group may have more than one Destinations
	// Only one destination will be used at one time
	destGrpNameMap = make(map[string][]Destination)

	// For finding clientSubscription quickly
	ClientSubscriptionNameMap = make(map[string]*clientSubscription)

	// map for storing clientSubscription users of the destination group
	DestGrp2ClientSubMap = make(map[string][]*clientSubscription)

	// map from clientSubscription to Destination Group name
	ClientSub2DestGrpNameMap = make(map[*clientSubscription]string)
)

type Destination struct {
	Addrs string
}

func (d Destination) Validate() error {
	if len(d.Addrs) == 0 {
		return errors.New("Destination.Addrs is empty")
	}
	// TODO: validate Addrs is in format IP:PORT
	return nil
}

// Global config for all clients
type ClientConfig struct {
	SrcIp          string
	RetryInterval  time.Duration
	Encoding       gpb.Encoding
	Unidirectional bool        // by default, no reponse from remote server
	TLS            *tls.Config // TLS config to use when connecting to target. Optional.
}

// clientSubscription is the container for config data,
// it also keeps mapping from destination to running publish Client instance
type clientSubscription struct {
	// Config Data
	name          string
	destGroupName string
	prefix        *gpb.Path
	paths         []*gpb.Path
	reportType    reportType
	interval      time.Duration // report interval

	// Running time data
	cMu    sync.Mutex
	client *Client
	stop   chan struct{} // Inform publishRun routine to stop
	//subRoutineChan chan struct{} // used for communication with sub-routine

	conTryCnt uint64 //Number of time trying to connect
	sendMsg   uint64
	recvMsg   uint64
}

// Client handles execution of the telemetry publish service.
type Client struct {
	conn *grpc.ClientConn

	mu      sync.Mutex
	client  spb.GNMIDialOutClient
	publish spb.GNMIDialOut_PublishClient

	// dataChan chan struct{} //to pass data struct pointer
	//
	// synced  sync.WaitGroup
	sendMsg uint64
	recvMsg uint64
}

func (cs *clientSubscription) Close() {
	if cs.stop != nil {
		close(cs.stop) //Inform the clientSubscription publish service routine to stop
	}
	cs.cMu.Lock()
	defer cs.cMu.Unlock()
	if cs.client != nil {
		cs.client.Close() // Close GNMIDialOutClient
	}
}

func (cs *clientSubscription) NewInstance(ctx context.Context) error {

	if cs.destGroupName == "" {
		log.V(2).Infof("Destination group is not set for %v", cs)
		return fmt.Errorf("Destination group is not set for %v", cs)
	}
	// Add this clientSubscription to the user list of Destination group
	DestGrp2ClientSubMap[cs.destGroupName] = append(DestGrp2ClientSubMap[cs.destGroupName], cs)

	dests, ok := destGrpNameMap[cs.destGroupName]
	if !ok {
		log.V(2).Infof("Destination group %v doesn't exist", cs.destGroupName)
		return fmt.Errorf("Destination group %v doesn't exist", cs.destGroupName)
	}

	// Connection to system database
	dc, err := sdc.NewDbClient(cs.paths, cs.prefix)
	if err != nil {
		log.V(1).Infof("Connection to DB for %v failed: %v", *cs, err)
		return fmt.Errorf("Connection to DB for %v failed: %v", *cs, err)
	}
	cs.stop = make(chan struct{}, 1)

	go publishRun(ctx, cs, dests, dc)

	return nil
}

// newClient returns a new initialized client.
// it connects to destination and publish service
// TODO: TLS credential support
func newClient(ctx context.Context, dest Destination) (*Client, error) {
	timeout := clientCfg.RetryInterval

	cancel := func() {}
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}
	if clientCfg.TLS != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(clientCfg.TLS)))
	}
	conn, err := grpc.DialContext(ctx, dest.Addrs, opts...)
	if err != nil {
		return nil, fmt.Errorf("Dial to (%s, timeout %v): %v", dest, timeout, err)
	}
	cl := spb.NewGNMIDialOutClient(conn)
	return &Client{
		conn:   conn,
		client: cl,
	}, nil
}

// Closing of client queue is triggered upon end of stream receive or stream error
// or fatal error of any client go routine .
// it will cause cancle of client context and exit of the send goroutines.
func (c *Client) Close() error {
	return c.conn.Close()
}

func publishRun(ctx context.Context, cs *clientSubscription, dests []Destination, dc *sdc.DbClient) {
	var err error
	var c *Client
	var destNum, destIdx int
	destNum = len(dests)
	destIdx = 0

	//ctx, cancel = context.WithTimeout(ctx, cs.interval)
	//defer cancel()

restart: //Remote server might go down, in that case we restart with next destination in the group
	cs.conTryCnt++
	dest := dests[destIdx]
	destIdx = (destIdx + 1) % destNum
	c, err = newClient(ctx, dest)
	if err != nil {
		select {
		case <-ctx.Done():
			log.V(1).Infof("%v connection: %v, cs.conTryCnt %v", dest, cs.name, err, cs.conTryCnt)
			return
		default:
		}
		log.V(1).Infof("Dialout connection for %v failed: %v, cs.conTryCnt %v", dest, cs.name, err, cs.conTryCnt)
		goto restart
	}

	pub, err := c.client.Publish(ctx)
	if err != nil {
		log.V(1).Infof("Publish to %v for %v failed: %v, retrying", dest, cs.name, err)
		c.Close()
		goto restart
	}
	// TODO: think about lock!!
	cs.cMu.Lock()
	cs.client = c
	cs.cMu.Unlock()

	if cs.reportType == Periodic {
		for {
			select {
			default:
				spbValues, err := dc.Get(nil)
				if err != nil {
					// TODO: need to inform
					log.V(2).Infof("Data read error %v for %v", err, cs)
					continue
					//return nil, status.Error(codes.NotFound, err.Error())
				}
				var updates []*gpb.Update
				var spbValue *spb.Value
				for _, spbValue = range spbValues {
					update := &gpb.Update{
						Path: spbValue.GetPath(),
						Val:  spbValue.GetVal(),
					}
					updates = append(updates, update)
				}
				rs := &gpb.SubscribeResponse_Update{
					Update: &gpb.Notification{
						Timestamp: spbValue.GetTimestamp(),
						Prefix:    cs.prefix,
						Update:    updates,
					},
				}
				response := &gpb.SubscribeResponse{Response: rs}

				log.V(6).Infof("cs %s sending \n\t%v \n To %s", cs.name, response, dest)
				err = pub.Send(response)
				if err != nil {
					log.V(1).Infof("Client %s pub Send error:%v, cs.conTryCnt %v", c, err, cs.conTryCnt)
					c.Close()
					// Retry
					goto restart
				}
				log.V(6).Infof("cs %s to  %s done", cs.name, dest)
				cs.sendMsg++
				c.sendMsg++

				time.Sleep(cs.interval)
			case <-cs.stop:
				// _, more := <-cs.stop
				// if !more {
				log.V(1).Infof("%v stop channel closed, exiting publishRun routine for destination %c", cs, dest)
				return
				//}
			}
		}
	} else {
		// TODO: change triggered streaming
	}
}

/*
	// telemetry client  global configuration
	Key         = TELEMETRY_CLIENT|Global
	src_ip      = IP
	retry_interval = 1*4DIGIT     ; In second
	encoding    = "JSON_IETF" / "ASCII" / "BYTES" / "PROTO"
	unidirectional = "true" / "false"    ; true by default

	// Destination group
	Key      = TELEMETRY_CLIENT|DestinationGroup_<name>
	dst_addr   = IP1:PORT2,IP2:PORT2       ;IP addresses separated by ","

	PORT = 1*5DIGIT
	IP = dec-octet "." dec-octet "." dec-octet "." dec-octet

	// Subscription group
	Key         = TELEMETRY_CLIENT|Subscription_<name>
	path_target = DbName
	paths       = PATH1,PATH2        ;PATH separated by ","
	dst_group   = <name>      ; // name of DestinationGroup
	report_type = "periodic" / "stream" / "once"
	report_interval = 1*8DIGIT      ; In millisecond,
*/

// clearDestGroupClient delete client instances for all clientSubscription using
// this Destination Group
func clearDestGroupClient(destGroupName string) {
	if css, ok := DestGrp2ClientSubMap[destGroupName]; ok {
		for _, cs := range css {
			cs.Close()
		}
	}
}

// setupDestGroupClients create client instances for all clientSubscription using
// this Destination Group
func setupDestGroupClients(ctx context.Context, destGroupName string) {
	if css, ok := DestGrp2ClientSubMap[destGroupName]; ok {
		for _, cs := range css {
			cs.NewInstance(ctx)
		}
	}
}

// start/stop/update telemetry publist client as requested
// TODO: more validation on db data
func processTelemetryClientConfig(ctx context.Context, redisDb *redis.Client, key string, op string) error {
	separator, _ := sdc.GetTableKeySeparator("CONFIG_DB")
	tableKey := "TELEMETRY_CLIENT" + separator + key
	fv, err := redisDb.HGetAll(tableKey).Result()
	if err != nil {
		log.V(2).Infof("redis HGetAll failed for %s with error %v", tableKey, err)
		return fmt.Errorf("redis HGetAll failed for %s with error %v", tableKey, err)
	}

	log.V(2).Infof("Processing %v %v", tableKey, fv)
	configMu.Lock()
	defer configMu.Unlock()

	if key == "Global" {
		if op == "hdel" {
			log.V(2).Infof("Invalid delete operation for %v", tableKey)
			return fmt.Errorf("Invalid delete operation for %v", tableKey)
		} else {
			for field, value := range fv {
				switch field {
				case "src_ip":
					clientCfg.SrcIp = value
				case "retry_interval":
					//TODO: check validity of the interval
					itvl, err := strconv.ParseUint(value, 10, 64)
					if err != nil {
						log.V(2).Infof("Invalid retry_interval %v %v", value, err)
						continue
					}
					clientCfg.RetryInterval = time.Second * time.Duration(itvl)
				case "encoding":
					//Flexible encoding Not supported yet
					clientCfg.Encoding = gpb.Encoding_JSON_IETF
				case "unidirectional":
					// No PublishResponse supported yet
					clientCfg.Unidirectional = true
				}
			}
			// TODO: Apply changes to all running instances
		}
	} else if strings.HasPrefix(key, "DestinationGroup_") {
		destGroupName := strings.TrimPrefix(key, "DestinationGroup_")
		if destGroupName == "" {
			return fmt.Errorf("Empty  Destination Group name %v", key)
		}
		//DestGrp2ClientSubMap
		if op == "hdel" {
			if _, ok := DestGrp2ClientSubMap[destGroupName]; ok {
				log.V(1).Infof("%v is being used: %v", destGroupName, DestGrp2ClientSubMap)
				return fmt.Errorf("%v is being used: %v", destGroupName, DestGrp2ClientSubMap)
			}
			delete(destGrpNameMap, destGroupName)
			log.V(3).Infof("Deleted  DestinationGroup %v", destGroupName)
			return nil
		} else {
			// Clear any client intances targeting this Destination group
			clearDestGroupClient(destGroupName)
			var dests []Destination
			for field, value := range fv {
				switch field {
				case "dst_addr":
					addrs := strings.Split(value, ",")
					for _, addr := range addrs {
						dst := Destination{Addrs: addr}
						if err = dst.Validate(); err != nil {
							log.V(2).Infof("Invalid destination address %v", addrs)
							return fmt.Errorf("Invalid destination address %v", addrs)
						}
						dests = append(dests, Destination{Addrs: addr})
					}
				default:
					log.V(2).Infof("Invalid DestinationGroup value %v", value)
					return fmt.Errorf("Invalid DestinationGroup value %v", value)
				}
			}
			destGrpNameMap[destGroupName] = dests
			setupDestGroupClients(ctx, destGroupName)
		}
	} else if strings.HasPrefix(key, "Subscription_") {
		name := strings.TrimPrefix(key, "Subscription_")
		if name == "" {
			return fmt.Errorf("Empty Subscription_ name %v", key)
		}
		csub, ok := ClientSubscriptionNameMap[name]
		if ok {
			csub.Close()
		}

		if op == "hdel" {
			var destGrpName string
			destGrpName, ok = ClientSub2DestGrpNameMap[csub]
			if ok {
				// Remove this ClientSubscrition from the list of the Destination group users
				csubs := DestGrp2ClientSubMap[destGrpName]
				for i, cs := range csubs {
					if reflect.DeepEqual(cs, csub) {
						csubs = append(csubs[:i], csubs[i+1:]...)
						break
					}
				}
				DestGrp2ClientSubMap[destGrpName] = csubs
			}
			// Delete clientSubscription from name map
			delete(ClientSubscriptionNameMap, name)
			log.V(3).Infof("Deleted  Client Subscription %v", name)
			return nil
		} else {
			// TODO: start one subscription publish routine for this request
			// Only start routine when DestGrp2ClientSubMap is not empty, or ...?
			cs := clientSubscription{
				interval: 5000, // default to 5000 milliseconds
				name:     name,
			}
			for field, value := range fv {
				switch field {
				case "dst_group":
					cs.destGroupName = value
				case "report_type":
					cs.reportType = NewReportType(value)
					if cs.reportType != Periodic {
						log.V(2).Infof("Report type %v not supported, fallback to %s", value, Periodic)
						cs.reportType = Periodic
					}
				case "report_interval":
					intvl, err := strconv.ParseUint(value, 10, 64)
					if err != nil {
						log.V(2).Infof("Invalid report_interval %v %v", value, err)
						continue
					}
					cs.interval = time.Duration(intvl) * time.Millisecond
				case "path_target":
					cs.prefix = &gpb.Path{
						Target: value,
					}
				case "paths":
					paths := strings.Split(value, ",")
					for _, path := range paths {
						pp, err := ygot.StringToPath(path, ygot.StructuredPath)
						if err != nil {
							log.V(2).Infof("Invalid paths %v", value)
							return fmt.Errorf("Invalid paths %v", value)
						}
						// append *gpb.Path
						cs.paths = append(cs.paths, pp)
					}
				default:
					log.V(2).Infof("Invalid field %v value %v", field, value)
					return fmt.Errorf("Invalid field %v value %v", field, value)
				}
			}
			cs.NewInstance(ctx)
		}
	}
	return nil
}

// read configDB data for telemetry client and start publishing service for client subscription
func DialOutRun(ctx context.Context, ccfg *ClientConfig) error {
	clientCfg = ccfg
	dbn := spb.Target_value["CONFIG_DB"]
	/*
		redisDb := redis.NewClient(&redis.Options{
			Network:     "unix",
			Addr:        sdc.Default_REDIS_UNIXSOCKET,
			Password:    "", // no password set
			DB:          int(dbn),
			DialTimeout: 0,
		})
	*/

	sdc.UseRedisLocalTcpPort = true
	redisDb := redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        sdc.Default_REDIS_LOCAL_TCP_PORT,
		Password:    "", // no password set
		DB:          int(dbn),
		DialTimeout: 0,
	})
	separator, _ := sdc.GetTableKeySeparator("CONFIG_DB")
	pattern := "__keyspace@" + strconv.Itoa(int(dbn)) + "__:TELEMETRY_CLIENT" + separator
	prefixLen := len(pattern)
	pattern += "*"

	pubsub := redisDb.PSubscribe(pattern)
	defer pubsub.Close()

	msgi, err := pubsub.ReceiveTimeout(time.Second)
	if err != nil {
		log.V(1).Infof("psubscribe to %s failed %v", pattern, err)
		return fmt.Errorf("psubscribe to %s failed %v", pattern, err)
	}
	subscr := msgi.(*redis.Subscription)
	if subscr.Channel != pattern {
		log.V(1).Infof("psubscribe to %s failed", pattern)
		return fmt.Errorf("psubscribe to %s", pattern)
	}
	log.V(2).Infof("Psubscribe succeeded for %v", subscr)

	var dbkeys []string
	dbkey_prefix := "TELEMETRY_CLIENT" + separator
	dbkeys, err = redisDb.Keys(dbkey_prefix + "*").Result()
	if err != nil {
		log.V(2).Infof("redis Keys failed for %v with err %v", pattern, err)
		return err
	}
	for _, dbkey := range dbkeys {
		dbkey = dbkey[len(dbkey_prefix):]
		processTelemetryClientConfig(ctx, redisDb, dbkey, "hset")
	}

	for {
		msgi, err := pubsub.ReceiveTimeout(time.Millisecond * 1000)
		if err != nil {
			neterr, ok := err.(net.Error)
			if ok {
				if neterr.Timeout() == true {
					continue
				}
			}
			log.V(2).Infof("pubsub.ReceiveTimeout err %v", err)
			continue
		}
		subscr := msgi.(*redis.Message)
		dbkey := subscr.Channel[prefixLen:]
		if subscr.Payload == "del" || subscr.Payload == "hdel" {
			processTelemetryClientConfig(ctx, redisDb, dbkey, "hdel")
		} else if subscr.Payload == "hset" {
			processTelemetryClientConfig(ctx, redisDb, dbkey, "hset")
		} else {
			log.V(2).Infof("Invalid psubscribe payload notification:  %v", subscr)
			continue
		}
	}
}
