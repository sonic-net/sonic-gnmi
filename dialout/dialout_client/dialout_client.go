package telemetry_dialout

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sonic-net/sonic-gnmi/internal/redisopts"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	sdc "github.com/sonic-net/sonic-gnmi/sonic_data_client"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"

	"github.com/Workiva/go-datastructures/queue"
	log "github.com/golang/glog"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"github.com/openconfig/ygot/ygot"
	"github.com/redis/go-redis/v9"
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

	// map for storing name of clientSubscription which are users of the destination group
	DestGrp2ClientSubMap = make(map[string][]string)

	// Track client connections that need reconnection due to network changes
	networkChangeTrigger = make(chan struct{}, 16)
)

var newRetryInterval = 30 * time.Second

// deviceInfoCache caches hostname and mgmt IP and updates dynamically
type deviceInfoCache struct {
	mu       sync.RWMutex
	hostname string
	mgmtIP   string
	updateCh chan struct{}
	stopCh   chan struct{}
	initOnce sync.Once // ensure init only once
}

var globalDeviceInfo = &deviceInfoCache{
	updateCh: make(chan struct{}, 16),
	stopCh:   make(chan struct{}),
}

// init starts monitoring
func init() {
	go globalDeviceInfo.startMonitoring()
}

// ensureInitialized ensures the cache is initialized
func (d *deviceInfoCache) ensureInitialized() {
	d.initOnce.Do(func() {
		d.refresh()
		log.V(2).Infof("Device info cache initialized with hostname: %s, mgmt IP: %s", d.hostname, d.mgmtIP)
	})
}

func (d *deviceInfoCache) startMonitoring() {
	// start monitoring
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-d.stopCh
		cancel()
	}()

	monitorHostnameAndMgmtIPChanges(ctx, d.updateCh)
}

func (d *deviceInfoCache) refresh() {
	hostname, mgmtIP, err := getHostnameAndMgmtIP()
	d.mu.Lock()
	defer d.mu.Unlock()

	if err != nil {
		log.V(2).Infof("Failed to refresh device info: %v", err)
		return
	}

	oldHostname := d.hostname
	oldMgmtIP := d.mgmtIP

	if hostname != "" && hostname != oldHostname {
		d.hostname = hostname
		log.V(2).Infof("Hostname updated from '%s' to '%s'", oldHostname, hostname)
	}

	if mgmtIP != "" && mgmtIP != oldMgmtIP {
		d.mgmtIP = mgmtIP
		log.V(2).Infof("Mgmt IP updated from '%s' to '%s'", oldMgmtIP, mgmtIP)

		// Trigger reconnection for all clients when mgmt IP changes
		select {
		case networkChangeTrigger <- struct{}{}:
			log.V(2).Infof("Network change detected, triggering reconnection for all clients")
		default:
		}
	}
}

func (d *deviceInfoCache) Get() (string, string) {
	// ensureInitialized
	d.ensureInitialized()

	// check for update
	select {
	case <-d.updateCh:
		d.refresh()
	default:
	}

	d.mu.RLock()
	defer d.mu.RUnlock()

	// default value is : unknown
	hostname := d.hostname
	if hostname == "" {
		hostname = "unknown"
	}

	mgmtIP := d.mgmtIP
	if mgmtIP == "" {
		mgmtIP = "unknown"
	}

	return hostname, mgmtIP
}

// getHostnameAndMgmtIP from db
func getHostnameAndMgmtIP() (string, string, error) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	stateDbn, err := sdcfg.GetDbId("STATE_DB", ns)
	if err != nil {
		return "", "", err
	}

	var redisDb *redis.Client
	if sdc.UseRedisLocalTcpPort == false {
		addr, err := sdcfg.GetDbSock("STATE_DB", ns)
		if err != nil {
			return "", "", err
		}
		optsUnix := redisopts.New(redis.Options{
			Network:     "unix",
			Addr:        addr,
			Password:    "", // no password set
			DB:          stateDbn,
			DialTimeout: 0,
		})
		redisDb = redis.NewClient(optsUnix)
	} else {
		addr, err := sdcfg.GetDbTcpAddr("STATE_DB", ns)
		if err != nil {
			return "", "", err
		}
		optsTcp := redisopts.New(redis.Options{
			Network:     "tcp",
			Addr:        addr,
			Password:    "", // no password set
			DB:          stateDbn,
			DialTimeout: 0,
		})
		redisDb = redis.NewClient(optsTcp)
	}
	defer redisDb.Close()

	// get IP from NETWORK_INFO_TABLE|eth0
	networkInfoKey := "NETWORK_INFO_TABLE|eth0"
	networkInfo, err := redisDb.HGetAll(context.Background(), networkInfoKey).Result()
	if err != nil {
		log.V(2).Infof("Failed to get network info from %s: %v", networkInfoKey, err)
		return "unknown", "unknown", err
	}

	mgmtIP, ipExists := networkInfo["ip"]
	if !ipExists {
		log.V(2).Infof("Failed to get device ip from : %v", networkInfo)
		mgmtIP = "unknown"
	}

	// get hostname from DEVICE_METADATA|localhost
	configDbn, err := sdcfg.GetDbId("CONFIG_DB", ns)
	if err != nil {
		return "", "", err
	}
	var configRedisDb *redis.Client
	if sdc.UseRedisLocalTcpPort == false {
		addr, err := sdcfg.GetDbSock("CONFIG_DB", ns)
		if err != nil {
			return "", "", err
		}
		optsUnix := redisopts.New(redis.Options{
			Network:     "unix",
			Addr:        addr,
			Password:    "", // no password set
			DB:          configDbn,
			DialTimeout: 0,
		})
		configRedisDb = redis.NewClient(optsUnix)
	} else {
		addr, err := sdcfg.GetDbTcpAddr("CONFIG_DB", ns)
		if err != nil {
			return "", "", err
		}
		optsTcp := redisopts.New(redis.Options{
			Network:     "tcp",
			Addr:        addr,
			Password:    "", // no password set
			DB:          configDbn,
			DialTimeout: 0,
		})
		configRedisDb = redis.NewClient(optsTcp)
	}
	defer configRedisDb.Close()

	deviceMetadataKey := "DEVICE_METADATA|localhost"
	deviceMetadata, err := configRedisDb.HGetAll(context.Background(), deviceMetadataKey).Result()
	if err != nil {
		log.V(2).Infof("Failed to get device metadata from %s: %v", deviceMetadataKey, err)
		return "unknown", "unknown", err
	}

	hostname, hostnameExists := deviceMetadata["hostname"]
	if !hostnameExists {
		log.V(2).Infof("Failed to get device hostname from : %v", deviceMetadata)
		hostname = "unknown"
	}

	log.V(3).Infof("Retrieved from db hostname: %s, mgmt IP: %s", hostname, mgmtIP)
	return hostname, mgmtIP, nil
}

// monitorHostnameAndMgmtIPChanges monitor changes of hostname and mgmtip
func monitorHostnameAndMgmtIPChanges(ctx context.Context, updateChan chan<- struct{}) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	stateDbn, err := sdcfg.GetDbId("STATE_DB", ns)
	if err != nil {
		log.V(1).Infof("Failed to get STATE_DB id: %v", err)
		return
	}

	var redisDb *redis.Client
	if sdc.UseRedisLocalTcpPort == false {
		addr, err := sdcfg.GetDbSock("STATE_DB", ns)
		if err != nil {
			log.V(1).Infof("Failed to get STATE_DB sock: %v", err)
			return
		}
		optsUnix := redisopts.New(redis.Options{
			Network:     "unix",
			Addr:        addr,
			Password:    "", // no password set
			DB:          stateDbn,
			DialTimeout: 0,
		})
		redisDb = redis.NewClient(optsUnix)
	} else {
		addr, err := sdcfg.GetDbTcpAddr("STATE_DB", ns)
		if err != nil {
			log.V(1).Infof("Failed to get STATE_DB tcp addr: %v", err)
			return
		}
		optsTcp := redisopts.New(redis.Options{
			Network:     "tcp",
			Addr:        addr,
			Password:    "", // no password set
			DB:          stateDbn,
			DialTimeout: 0,
		})
		redisDb = redis.NewClient(optsTcp)
	}
	defer redisDb.Close()

	pattern := "__keyspace@" + strconv.Itoa(int(stateDbn)) + "__:NETWORK_INFO_TABLE|eth0"
	pubsub := redisDb.PSubscribe(ctx, pattern)
	defer pubsub.Close()

	msgi, err := pubsub.ReceiveTimeout(ctx, time.Second)
	if err != nil {
		log.V(1).Infof("psubscribe to %s failed %v", pattern, err)
		return
	}
	subscr := msgi.(*redis.Subscription)
	if subscr.Channel != pattern {
		log.V(1).Infof("psubscribe to %s failed", pattern)
		return
	}
	log.V(2).Infof("Psubscribe to network info succeeded: %v", subscr)

	configDbn, err := sdcfg.GetDbId("CONFIG_DB", ns)
	if err != nil {
		log.V(1).Infof("Failed to get CONFIG_DB id: %v", err)
		return
	}
	var configRedisDb *redis.Client
	if sdc.UseRedisLocalTcpPort == false {
		addr, err := sdcfg.GetDbSock("CONFIG_DB", ns)
		if err != nil {
			log.V(1).Infof("Failed to get CONFIG_DB sock: %v", err)
			return
		}
		optsUnix := redisopts.New(redis.Options{
			Network:     "unix",
			Addr:        addr,
			Password:    "", // no password set
			DB:          configDbn,
			DialTimeout: 0,
		})
		configRedisDb = redis.NewClient(optsUnix)
	} else {
		addr, err := sdcfg.GetDbTcpAddr("CONFIG_DB", ns)
		if err != nil {
			log.V(1).Infof("Failed to get CONFIG_DB tcp addr: %v", err)
			return
		}
		optsTcp := redisopts.New(redis.Options{
			Network:     "tcp",
			Addr:        addr,
			Password:    "", // no password set
			DB:          configDbn,
			DialTimeout: 0,
		})
		configRedisDb = redis.NewClient(optsTcp)
	}
	defer configRedisDb.Close()

	configPattern := "__keyspace@" + strconv.Itoa(int(configDbn)) + "__:DEVICE_METADATA|localhost"
	configPubsub := configRedisDb.PSubscribe(ctx, configPattern)
	defer configPubsub.Close()

	configMsgi, err := configPubsub.ReceiveTimeout(ctx, time.Second)
	if err != nil {
		log.V(1).Infof("psubscribe to %s failed %v", configPattern, err)
		return
	}
	configSubscr := configMsgi.(*redis.Subscription)
	if configSubscr.Channel != configPattern {
		log.V(1).Infof("psubscribe to %s failed", configPattern)
		return
	}
	log.V(2).Infof("Psubscribe to device metadata succeeded: %v", configSubscr)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := pubsub.ReceiveTimeout(ctx, time.Millisecond*100)
			if err == nil {
				if subMsg, ok := msg.(*redis.Message); ok {
					if subMsg.Payload == "del" || subMsg.Payload == "hset" || subMsg.Payload == "hdel" {
						log.V(3).Infof("Network info changed: %v", subMsg)
						select {
						case updateChan <- struct{}{}:
							log.V(3).Infof("Sent update notification for network info")
						default:
							log.V(3).Infof("Update channel full, skipping notification")
						}
					}
				}
			}

			configMsg, err := configPubsub.ReceiveTimeout(ctx, time.Millisecond*100)
			if err == nil {
				if subMsg, ok := configMsg.(*redis.Message); ok {
					if subMsg.Payload == "del" || subMsg.Payload == "hset" || subMsg.Payload == "hdel" {
						log.V(3).Infof("Device metadata changed: %v", subMsg)
						select {
						case updateChan <- struct{}{}:
							log.V(3).Infof("Sent update notification for device metadata")
						default:
							log.V(3).Infof("Update channel full, skipping notification")
						}
					}
				}
			}
		}
	}
}

// addDeviceInfoToResp add DeviceInfo into Path of resp
func addDeviceInfoToResp(resp *gpb.SubscribeResponse) {
	if resp == nil {
		return
	}
	// skip SyncResponse
	if resp.GetSyncResponse() {
		return
	}
	deviceInfo := &gpb.PathElem{
		Name: "DeviceInfo",
		Key:  map[string]string{},
	}

	hostname, mgmtIP := globalDeviceInfo.Get()
	deviceInfo.Key["HostName"] = hostname
	deviceInfo.Key["MgmtIp"] = mgmtIP

	if u := resp.GetUpdate(); u != nil && u.Update != nil {
		for _, up := range u.Update {
			if up != nil && up.Path != nil {
				tmpPath := &gpb.Path{}
				tmpPath.Elem = append(up.Path.Elem, deviceInfo)
				up.Path = tmpPath
			}
		}
	}
}

// Add function to handle network change reconnection
func handleNetworkChange(ctx context.Context) {
	for {
		select {
		case <-networkChangeTrigger:
			log.V(1).Infof("Network change detected, reconnecting all clients")

			configMu.Lock()
			// Close and reopen all client subscriptions
			for name, cs := range ClientSubscriptionNameMap {
				log.V(1).Infof("Reconnecting client %s due to network change", name)
				cs.Close()
				cs.cancel()

				// Recreate context
				newCtx, cancel := context.WithCancel(context.Background())
				cs.cancel = cancel

				// Reinitialize
				err := cs.NewInstance(newCtx)
				if err != nil {
					log.V(1).Infof("Failed to reconnect client %s after network change: %v", name, err)
				}
			}
			configMu.Unlock()

		case <-ctx.Done():
			return
		}
	}
}

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
	RedisConType   string      // "unix"  or "tcp"
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
	cMu     sync.Mutex
	clients map[string]*Client   // GNMIDialOutClient map, key as dest addr
	dc      sdc.Client           // SONiC data client
	stop    chan struct{}        // Inform publishRun routine to stop
	q       *queue.PriorityQueue // for data passing among go routine
	w       sync.WaitGroup       // Wait for all sub go routine to finish
	opened  bool                 // whether there is opened instance for this client subscription
	cancel  context.CancelFunc

	conTryCnt uint64 //Number of time trying to connect
	sendMsg   uint64
	recvMsg   uint64
	errors    uint64
}

// Client handles execution of the telemetry publish service.
type Client struct {
	conn *grpc.ClientConn

	mu      sync.Mutex
	client  spb.GNMIDialOutClient
	publish spb.GNMIDialOut_PublishClient
	dest    string // destination address

	sendMsg uint64
	recvMsg uint64
}

func (cs *clientSubscription) Close() {
	cs.cMu.Lock()
	defer cs.cMu.Unlock()
	if cs.opened == false {
		log.V(5).Infof("Opened is false: %v", cs)
		return
	}
	if cs.stop != nil {
		close(cs.stop) //Inform the clientSubscription publish service routine to stop
	}

	if cs.q != nil {
		if !cs.q.Disposed() {
			cs.q.Dispose()
		}
	}

	// Close all clients
	for addr, client := range cs.clients {
		log.V(2).Infof("Closing client for %s", addr)
		client.Close()
	}
	cs.clients = make(map[string]*Client)
	cs.opened = false
	log.V(2).Infof("Closed %v", cs)
}

func (cs *clientSubscription) NewInstance(ctx context.Context) error {
	cs.cMu.Lock()
	defer cs.cMu.Unlock()

	if cs.destGroupName == "" {
		log.V(2).Infof("Destination group is not set for %v", cs)
		return fmt.Errorf("Destination group is not set for %v", cs)
	}

	dests, ok := destGrpNameMap[cs.destGroupName]
	if !ok {
		log.V(2).Infof("Destination group %v doesn't exist", cs.destGroupName)
		return fmt.Errorf("Destination group %v doesn't exist", cs.destGroupName)
	}

	target := cs.prefix.GetTarget()
	if target == "" {
		return fmt.Errorf("Empty target data not supported yet")
	}

	// Connection to system data source
	var dc sdc.Client
	var err error
	if target == "OTHERS" {
		dc, err = sdc.NewNonDbClient(cs.paths, cs.prefix)
	} else if target == "OC_YANG" {
		dc, err = sdc.NewTranslClient(cs.prefix, cs.paths, ctx, nil, sdc.TranslWildcardOption{})
	} else {
		dc, err = sdc.NewDbClient(cs.paths, cs.prefix)
	}
	if err != nil {
		log.V(1).Infof("Connection to DB for %v failed: %v", *cs, err)
		return fmt.Errorf("Connection to DB for %v failed: %v", *cs, err)
	}
	cs.dc = dc
	go publishRun(ctx, cs, dests)
	log.V(2).Infof("publishRun for %v with destination %v", cs, dests)
	return nil
}

// send runs until process Queue returns an error ( Periodic )
func (cs *clientSubscription) send(stream spb.GNMIDialOut_PublishClient, client *Client) error {
	var lastUpdTimeSec float64 = 0
	var threshold float64 = 10
	for {
		items, err := cs.q.Get(1)

		if items == nil {
			log.V(1).Infof("%v", err)
			return err
		}
		if err != nil {
			cs.errors++
			log.V(1).Infof("%v", err)
			return fmt.Errorf("unexpected queue Get(1): %v", err)
		}

		var resp *gpb.SubscribeResponse
		switch v := items[0].(type) {
		case sdc.Value:
			if resp, err = sdc.ValToResp(v); err != nil {
				cs.errors++
				return err
			}
		default:
			log.V(1).Infof("Unknown data type %v for %s in queue", items[0], cs)
			cs.errors++
			continue
		}

		cs.sendMsg++
		client.sendMsg++
		addDeviceInfoToResp(resp)
		log.V(6).Infof("cs %s sending to %s resp after addDeviceInfoToResp \n\t%v ", cs.name, client.dest, resp)
		err = stream.Send(resp)
		if err != nil {
			log.V(1).Infof("Client %s to %s sending error:%v", cs, client.dest, err)
			cs.errors++
			return err
		}

		if strings.Contains(cs.name, "METADATA") || strings.Contains(cs.destGroupName, "METADATA") {
			if update := resp.GetUpdate(); update != nil {
				updateTsSec := time.Duration(update.Timestamp).Seconds()
				diffTime := updateTsSec - lastUpdTimeSec
				if lastUpdTimeSec == 0 || diffTime >= threshold {
					log.V(2).Infof("### METADATA related Client %s to %s done sending, msg count %d, msg %v", cs, client.dest, client.sendMsg, resp)
					log.V(2).Infof("### METADATA related Client diffTime(%v), threshold(%v), updateTsSec(%v), curTimeSec(%v)", diffTime, threshold, updateTsSec, lastUpdTimeSec)
					lastUpdTimeSec = updateTsSec
				}
			}
		} else {
			log.V(3).Infof("### Client %s to %s done sending, msg count %d, msg %v", cs, client.dest, client.sendMsg, resp)
		}
	}
}

// streamSend (Stream mode) with reconnection support
func (cs *clientSubscription) streamSend(failedAddrs map[string]bool, reconnectTrigger chan string) error {
	var lastUpdTimeSec float64 = 0
	var threshold float64 = 10

	for {
		items, err := cs.q.Get(1)

		if items == nil {
			log.V(1).Infof("%v", err)
			return err
		}
		if err != nil {
			cs.errors++
			log.V(1).Infof("%v", err)
			return fmt.Errorf("unexpected queue Get(1): %v", err)
		}

		var resp *gpb.SubscribeResponse
		switch v := items[0].(type) {
		case sdc.Value:
			if resp, err = sdc.ValToResp(v); err != nil {
				cs.errors++
				return err
			}
		default:
			log.V(1).Infof("Unknown data type %v for %s in queue", items[0], cs)
			cs.errors++
			continue
		}

		addDeviceInfoToResp(resp)

		// Send to all clients
		cs.cMu.Lock()
		for addr, client := range cs.clients {

			log.V(6).Infof("cs %s sending to %s resp after addDeviceInfoToResp \n\t%v ", cs.name, addr, resp)
			err = client.publish.Send(resp)
			if err != nil {
				log.V(1).Infof("Client %s to %s sending error:%v", cs, addr, err)
				cs.errors++
				failedAddrs[addr] = true
				// Trigger immediate reconnection
				select {
				case reconnectTrigger <- addr:
					log.V(1).Infof("Triggered reconnection for %s", addr)
				default:
					log.V(1).Infof("Reconnection trigger channel full for %s", addr)
				}
			} else {
				cs.sendMsg++
				client.sendMsg++

				// Logging
				if strings.Contains(cs.name, "METADATA") || strings.Contains(cs.destGroupName, "METADATA") {
					if resp == nil {
						continue
					}
					update := resp.GetUpdate()
					if update == nil {
						continue
					}
					duration := time.Duration(update.Timestamp)
					updateTsSec := duration.Seconds()
					diffTime := updateTsSec - lastUpdTimeSec
					if lastUpdTimeSec == 0 || diffTime >= threshold {
						log.V(2).Infof("### METADATA related Client %s to %s done sending, msg count %d, msg %v",
							cs, addr, client.sendMsg, resp)
						log.V(2).Infof("### METADATA related Client diffTime(%v), threshold(%v), updateTsSec(%v), curTimeSec(%v)",
							diffTime, threshold, updateTsSec, lastUpdTimeSec)
						lastUpdTimeSec = updateTsSec
					}
				} else {
					log.V(3).Infof("### Client %s to %s done sending, msg count %d, msg %v",
						cs, addr, client.sendMsg, resp)
				}
			}
		}
		cs.cMu.Unlock()
	}
}

// String returns the target the client is querying.
func (cs *clientSubscription) String() string {
	return fmt.Sprintf(" %s:%s:%s prefix %v paths %v interval %v, sendMsg %v, recvMsg %v",
		cs.name, cs.destGroupName, cs.reportType, cs.prefix.GetTarget(), cs.paths, cs.interval, cs.sendMsg, cs.recvMsg)
}

// newClient returns a new initialized GNMIDialout client.
// it connects to destination and publish service
// TODO: TLS credential support
func newClient(ctx context.Context, addr string) (*Client, error) {
	timeout := clientCfg.RetryInterval
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}
	if clientCfg.TLS != nil {
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(clientCfg.TLS)))
	}
	conn, err := grpc.DialContext(ctx, addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("Dial to (%s, timeout %v): %v", addr, timeout, err)
	}
	cl := spb.NewGNMIDialOutClient(conn)
	return &Client{
		conn:   conn,
		client: cl,
		dest:   addr,
	}, nil
}

// Closing of client queue is triggered upon end of stream receive or stream error
// or fatal error of any client go routine .
// it will cause cancle of client context and exit of the send goroutines.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// Helper function for reconnection attempts
func attemptReconnection(ctx context.Context, cs *clientSubscription, addr string, failedAddrs *map[string]bool, reconnectTrigger chan string) {
	log.V(3).Infof("Attempting to reconnect to %s for %v", addr, cs.name)

	c, err := newClient(ctx, addr)
	if err != nil {
		log.V(1).Infof("Reconnection to %s failed for %v: %v", addr, cs.name, err)
		return
	}

	pub, err := c.client.Publish(ctx)
	if err != nil {
		log.V(1).Infof("Publish to %s for %v failed during reconnection: %v", addr, cs.name, err)
		c.Close()
		return
	}
	c.publish = pub

	cs.cMu.Lock()
	cs.clients[addr] = c
	delete(*failedAddrs, addr)
	cs.cMu.Unlock()

	log.V(1).Infof("Reconnected to %s successfully for %v", addr, cs.name)

	if cs.reportType == Periodic {
		cs.w.Add(1)
		go func(client *Client) {
			defer cs.w.Done()
			defer func() {
				cs.cMu.Lock()
				delete(cs.clients, client.dest)
				(*failedAddrs)[client.dest] = true
				cs.cMu.Unlock()
				client.Close()
				// Trigger reconnection on exit
				select {
				case reconnectTrigger <- client.dest:
				default:
				}
			}()
			err = cs.send(pub, client)
			if err != nil {
				log.V(1).Infof("Client %v to %s send error after reconnection: %v", cs.name, client.dest, err)
			}
		}(c)
	} else if cs.reportType == Stream {
		// For Stream mode, just add the client to the map
		// The streamSend goroutine will handle sending to all clients
		log.V(1).Infof("Stream mode: Added reconnected client for %s", addr)
	}
}

func publishRun(ctx context.Context, cs *clientSubscription, dests []Destination) {
	cs.cMu.Lock()
	cs.stop = make(chan struct{}, 1)
	cs.q = queue.NewPriorityQueue(1, false)
	cs.opened = true
	cs.clients = make(map[string]*Client)
	cs.cMu.Unlock()

	cs.conTryCnt++

	// Map to track failed addresses that need reconnection
	failedAddrs := make(map[string]bool)

	// Channel to trigger immediate reconnection
	reconnectTrigger := make(chan string, 16)

	// Channel to detect network changes for this specific client
	clientNetworkChange := make(chan struct{}, 1)

	// Monitor network changes for this client
	go func() {
		for {
			select {
			case <-networkChangeTrigger:
				log.V(1).Infof("Network change detected for client %s, preparing reconnection", cs.name)
				select {
				case clientNetworkChange <- struct{}{}:
				default:
				}
			case <-cs.stop:
				return
			}
		}
	}()

	// Create connections to all destinations
	for _, dest := range dests {
		addr := dest.Addrs
		go func(addr string) {
			c, err := newClient(ctx, addr)
			if err != nil {
				log.V(1).Infof("Dialout connection for %s failed for %v, %v cs.conTryCnt %v", addr, cs.name, err, cs.conTryCnt)
				cs.cMu.Lock()
				failedAddrs[addr] = true
				cs.cMu.Unlock()
				select {
				case reconnectTrigger <- addr:
				default:
				}
				return
			}

			pub, err := c.client.Publish(ctx)
			if err != nil {
				log.V(1).Infof("Publish to %s for %v failed: %v", addr, cs.name, err)
				c.Close()
				cs.cMu.Lock()
				failedAddrs[addr] = true
				cs.cMu.Unlock()
				select {
				case reconnectTrigger <- addr:
				default:
				}
				return
			}
			c.publish = pub

			cs.cMu.Lock()
			cs.clients[addr] = c
			delete(failedAddrs, addr)
			cs.cMu.Unlock()

			log.V(1).Infof("Dialout service connected to %s successfully for %v", addr, cs.name)

			if cs.reportType == Periodic {
				cs.w.Add(1)
				go func(client *Client) {
					defer cs.w.Done()
					defer func() {
						cs.cMu.Lock()
						delete(cs.clients, client.dest)
						failedAddrs[client.dest] = true
						cs.cMu.Unlock()
						client.Close()
						// Trigger reconnection on exit
						select {
						case reconnectTrigger <- client.dest:
						default:
						}
					}()
					err = cs.send(pub, client)
					if err != nil {
						log.V(1).Infof("Client %v to %s send error: %v", cs.name, client.dest, err)
					}
				}(c)
			}
		}(addr)
	}

	// Dedicated reconnection goroutine with network change handling
	go func() {
		ticker := time.NewTicker(newRetryInterval)
		defer ticker.Stop()

		for {
			select {
			case addr := <-reconnectTrigger:
				log.V(3).Infof("Immediate reconnection triggered for %s", addr)
				go attemptReconnection(ctx, cs, addr, &failedAddrs, reconnectTrigger)

			case <-clientNetworkChange:
				log.V(1).Infof("Network change detected for client %s, reconnecting all destinations", cs.name)
				// Close all existing connections
				cs.cMu.Lock()
				for addr, client := range cs.clients {
					client.Close()
					delete(cs.clients, addr)
					failedAddrs[addr] = true
				}
				cs.cMu.Unlock()

				// Trigger reconnection for all addresses
				for _, dest := range dests {
					select {
					case reconnectTrigger <- dest.Addrs:
						log.V(1).Infof("NetworkChange Immediate reconnection triggered for %v", dest.Addrs)
					default:
					}
				}

			case <-ticker.C:
				// Periodic check for failed addresses
				cs.cMu.Lock()
				for addr := range failedAddrs {
					if _, exists := cs.clients[addr]; !exists {
						log.V(1).Infof("Periodic reconnection check for %s", addr)
						go attemptReconnection(ctx, cs, addr, &failedAddrs, reconnectTrigger)
					}
				}
				cs.cMu.Unlock()

			case <-cs.stop:
				return
			}
		}
	}()

	// Wait a bit for connections to establish
	time.Sleep(100 * time.Millisecond)

	switch cs.reportType {
	case Periodic:
		for {
			select {
			default:
				spbValues, err := cs.dc.Get(nil)
				if err != nil {
					log.V(2).Infof("Data read error %v for %v", err, cs)
					time.Sleep(cs.interval)
					continue
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

				// Send to all clients
				cs.cMu.Lock()
				for addr, client := range cs.clients {

					log.V(6).Infof("cs %s sending \n\t%v \n To %s", cs.name, response, addr)
					err = client.publish.Send(response)
					if err != nil {
						log.V(1).Infof("Client %v to %s pub Send error:%v, cs.conTryCnt %v", cs.name, addr, err, cs.conTryCnt)
						failedAddrs[addr] = true
						// Trigger immediate reconnection on send error
						select {
						case reconnectTrigger <- addr:
						default:
						}
					} else {
						cs.sendMsg++
						client.sendMsg++
						delete(failedAddrs, addr)
					}
				}
				cs.cMu.Unlock()

				log.V(6).Infof("cs %s done sending to all destinations", cs.name)

				time.Sleep(cs.interval)
			case <-cs.stop:
				log.V(1).Infof("%v exiting publishRun routine", cs)
				return
			}
		}
	case Stream:
		log.V(1).Infof("### publishRun Stream cs.name(%v)", cs.name)

		cs.w.Add(1)
		go cs.dc.StreamRun(cs.q, cs.stop, &cs.w, nil)

		time.Sleep(100 * time.Millisecond)

		sendErrChan := make(chan error, 1)

		cs.w.Add(1)
		go func() {
			defer cs.w.Done()
			// Pass failedAddrs and reconnectTrigger to streamSend
			err := cs.streamSend(failedAddrs, reconnectTrigger)
			if err != nil {
				log.V(1).Infof("Client %v stream send error: %v, cs.conTryCnt %v", cs.name, err, cs.conTryCnt)
				select {
				case sendErrChan <- err:
				default:
				}
			}
		}()

		select {
		case <-sendErrChan:
			log.V(1).Infof("%v stream send error, reconnection triggered", cs.name)
		case <-cs.stop:
			log.V(1).Infof("%v exiting publishRun routine", cs)
			return
		}
	default:
		log.V(1).Infof("Unsupported report type %s in %v ", cs.reportType, cs)
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

	The sonic-telemetry_client YANG model declares the list key as "prefix name",
	so YANG-validated tooling (config replace, GCU, golden config) spells the same
	rows TELEMETRY_CLIENT|DestinationGroup|<name> and TELEMETRY_CLIENT|Subscription|<name>.
	Both spellings are accepted; see matchRowPrefix.
*/

// closeDestGroupClient close client instances for all clientSubscription using
// this Destination Group
func closeDestGroupClient(destGroupName string) {
	if names, ok := DestGrp2ClientSubMap[destGroupName]; ok {
		for _, name := range names {
			cs := ClientSubscriptionNameMap[name]
			cs.Close()
			cs.cancel()
		}
	}
}

// setupDestGroupClients create client instances for all clientSubscription using
// this Destination Group
func setupDestGroupClients(ctx context.Context, destGroupName string) {
	if names, ok := DestGrp2ClientSubMap[destGroupName]; ok {
		for _, name := range names {
			// Create a copy of Client subscription, existing one might be closing, don't interfere with it.
			cs := *ClientSubscriptionNameMap[name]
			log.V(2).Infof("NewInstance with destGroup change for %s to %s", name, destGroupName)
			cs.NewInstance(ctx)
			ClientSubscriptionNameMap[name] = &cs
		}
	}
}

// matchRowPrefix matches a TELEMETRY_CLIENT row key against a row type and returns
// the row name. The historical spelling joins the row type and the name with "_"
// (DestinationGroup_HS); the YANG model declares key "prefix name", so config
// written through YANG-validated tooling joins them with the CONFIG_DB key
// separator instead (DestinationGroup|HS). Accept both, so hand written config
// keeps working and dial-out becomes configurable through config replace / GCU.
func matchRowPrefix(key string, rowType string, separator string) (string, bool) {
	for _, sep := range []string{"_", separator} {
		if strings.HasPrefix(key, rowType+sep) {
			return strings.TrimPrefix(key, rowType+sep), true
		}
	}
	return "", false
}

// start/stop/update telemetry publist client as requested
// TODO: more validation on db data
func processTelemetryClientConfig(ctx context.Context, redisDb *redis.Client, key string, op string) error {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	separator, err := sdc.GetTableKeySeparator("CONFIG_DB", ns)
	if err != nil {
		return err
	}
	tableKey := "TELEMETRY_CLIENT" + separator + key
	fv, err := redisDb.HGetAll(context.Background(), tableKey).Result()
	if err != nil {
		log.V(2).Infof("redis HGetAll failed for %s with error %v", tableKey, err)
		return fmt.Errorf("redis HGetAll failed for %s with error %v", tableKey, err)
	}

	log.V(2).Infof("Processing %v %v", tableKey, fv)
	configMu.Lock()
	defer configMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)

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
			// Apply changes to all running instances
			for grpName := range destGrpNameMap {
				closeDestGroupClient(grpName)
				setupDestGroupClients(ctx, grpName)
			}
		}
	} else if destGroupName, matched := matchRowPrefix(key, "DestinationGroup", separator); matched {
		if destGroupName == "" {
			return fmt.Errorf("Empty  Destination Group name %v", key)
		}
		// Close any client intances targeting this Destination group
		closeDestGroupClient(destGroupName)
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
	} else if name, matched := matchRowPrefix(key, "Subscription", separator); matched {
		if name == "" {
			return fmt.Errorf("Empty Subscription name %v", key)
		}
		csub, ok := ClientSubscriptionNameMap[name]
		if ok {
			csub.Close()
			csub.cancel()
		}

		if op == "hdel" {
			destGrpName := csub.destGroupName
			// Remove this ClientSubscrition from the list of the Destination group users
			csNames := DestGrp2ClientSubMap[destGrpName]
			for i, csName := range csNames {
				if name == csName {
					csNames = append(csNames[:i], csNames[i+1:]...)
					break
				}
			}
			DestGrp2ClientSubMap[destGrpName] = csNames
			// Delete clientSubscription from name map
			delete(ClientSubscriptionNameMap, name)
			log.V(3).Infof("Deleted  Client Subscription %v", name)
			return nil
		} else {
			// TODO: start one subscription publish routine for this request
			// Only start routine when DestGrp2ClientSubMap is not empty, or ...?
			cs := clientSubscription{
				interval: 5000 * time.Millisecond, // default to 5000 milliseconds
				name:     name,
				cancel:   cancel,
			}
			for field, value := range fv {
				switch field {
				case "dst_group":
					cs.destGroupName = value
				case "report_type":
					cs.reportType = NewReportType(value)
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
					ps := strings.Split(value, ",")
					newPaths := []*gpb.Path{}
					for _, p := range ps {
						pp, err := ygot.StringToPath(p, ygot.StructuredPath)
						if err != nil {
							log.V(2).Infof("Invalid paths %v", value)
							return fmt.Errorf("Invalid paths %v", value)
						}
						// append *gpb.Path
						newPaths = append(newPaths, pp)
					}
					cs.paths = newPaths
				default:
					log.V(2).Infof("Invalid field %v value %v", field, value)
					return fmt.Errorf("Invalid field %v value %v", field, value)
				}
			}
			log.V(2).Infof("New clientSubscription %v", cs)
			if cs.destGroupName == "" {
				// not destination configured, just return
				return nil
			}

			var found bool
			for _, na := range DestGrp2ClientSubMap[cs.destGroupName] {
				if na == cs.name {
					found = true
					break
				}
			}
			if !found {
				// Add this clientSubscription to the user list of Destination group
				DestGrp2ClientSubMap[cs.destGroupName] = append(DestGrp2ClientSubMap[cs.destGroupName], cs.name)
			}
			ClientSubscriptionNameMap[cs.name] = &cs
			log.V(2).Infof("NewInstance with Subscription change for %s to %s", cs.name, cs.destGroupName)
			cs.NewInstance(ctx)
		}
	}
	return nil
}

// read configDB data for telemetry client and start publishing service for client subscription
func DialOutRun(ctx context.Context, ccfg *ClientConfig) error {
	clientCfg = ccfg

	// Start network change handler
	go handleNetworkChange(ctx)

	ns, _ := sdcfg.GetDbDefaultNamespace()
	dbn, err := sdcfg.GetDbId("CONFIG_DB", ns)
	if err != nil {
		return err
	}

	var redisDb *redis.Client
	if sdc.UseRedisLocalTcpPort == false {
		addr, err := sdcfg.GetDbSock("CONFIG_DB", ns)
		if err != nil {
			return err
		}
		optsUnix := redisopts.New(redis.Options{
			Network:     "unix",
			Addr:        addr,
			Password:    "", // no password set
			DB:          dbn,
			DialTimeout: 0,
		})
		redisDb = redis.NewClient(optsUnix)
	} else {
		addr, err := sdcfg.GetDbTcpAddr("CONFIG_DB", ns)
		if err != nil {
			return err
		}
		optsTcp := redisopts.New(redis.Options{
			Network:     "tcp",
			Addr:        addr,
			Password:    "", // no password set
			DB:          dbn,
			DialTimeout: 0,
		})
		redisDb = redis.NewClient(optsTcp)
	}

	separator, err := sdc.GetTableKeySeparator("CONFIG_DB", ns)
	if err != nil {
		return err
	}
	pattern := "__keyspace@" + strconv.Itoa(int(dbn)) + "__:TELEMETRY_CLIENT" + separator
	prefixLen := len(pattern)
	pattern += "*"

	pubsub := redisDb.PSubscribe(context.Background(), pattern)
	defer pubsub.Close()

	msgi, err := pubsub.ReceiveTimeout(context.Background(), time.Second)
	if err != nil {
		log.V(1).Infof("psubscribe to %s failed %v", pattern, err)
		return fmt.Errorf("psubscribe to %s failed %v", pattern, err)
	}
	subscr := msgi.(*redis.Subscription)
	if subscr.Channel != pattern {
		log.V(1).Infof("psubscribe to %s failed", pattern)
		return fmt.Errorf("psubscribe to %s", pattern)
	}
	log.V(2).Infof("Psubscribe succeeded: %v", subscr)

	var dbkeys []string
	dbkey_prefix := "TELEMETRY_CLIENT" + separator
	dbkeys, err = redisDb.Keys(context.Background(), dbkey_prefix+"*").Result()
	if err != nil {
		log.V(2).Infof("redis Keys failed for %v with err %v", pattern, err)
		return err
	}
	for _, dbkey := range dbkeys {
		dbkey = dbkey[len(dbkey_prefix):]
		processTelemetryClientConfig(ctx, redisDb, dbkey, "hset")
	}

	for {
		msgi, err := pubsub.ReceiveTimeout(context.Background(), time.Millisecond*1000)
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
		// Check if ctx was canceled.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}
