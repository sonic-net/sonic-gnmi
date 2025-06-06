package gnmi

import (
	log "github.com/golang/glog"
	"net"
	"regexp"
	"sync"
	"time"

	"github.com/go-redis/redis"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"
)

const table = "TELEMETRY_CONNECTIONS"

type ConnectionManager struct {
	connections map[string]struct{}
	mu          sync.RWMutex
	threshold   int
	rclient     *redis.Client
}

func CreateConnectionManager(threshold int) *ConnectionManager {
	cm := &ConnectionManager{
		connections: make(map[string]struct{}),
		threshold:   threshold,
	}
	cm.PrepareRedis()
	return cm
}

func (cm *ConnectionManager) GetThreshold() int {
	return cm.threshold
}

func (cm *ConnectionManager) PrepareRedis() {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	addr, err := sdcfg.GetDbTcpAddr("STATE_DB", ns)
	if err != nil {
		log.Errorf("Addr err: %v", err)
		return
	}
	db, err := sdcfg.GetDbId("STATE_DB", ns)
	if err != nil {
		log.Errorf("DB err: %v", err)
		return
	}
	cm.rclient = redis.NewClient(&redis.Options{
		Network:     "tcp",
		Addr:        addr,
		Password:    "",
		DB:          db,
		DialTimeout: 0,
	})

	res, _ := cm.rclient.HGetAll("TELEMETRY_CONNECTIONS").Result()

	if res == nil {
		return
	}

	for key, _ := range res {
		cm.rclient.HDel(table, key)
	}
}

func (cm *ConnectionManager) Add(addr net.Addr, query string) (string, bool) {
	cm.mu.RLock()                                                 // reading
	if len(cm.connections) >= cm.threshold && cm.threshold != 0 { // 0 is defined as no threshold
		log.V(1).Infof("Cannot add another client connection as threshold is already at limit")
		cm.mu.RUnlock()
		return "", false
	}
	cm.mu.RUnlock()
	key := createKey(addr, query)
	log.V(1).Infof("Adding client connection: %s", key)
	cm.mu.Lock() // writing
	cm.connections[key] = struct{}{}
	cm.mu.Unlock()
	cm.storeKeyRedis(key)
	return key, true
}

func (cm *ConnectionManager) Remove(key string) bool {
	cm.mu.RLock() // reading
	_, exists := cm.connections[key]
	cm.mu.RUnlock()
	if exists {
		log.V(1).Infof("Closing connection: %s", key)
		cm.mu.Lock() // writing
		delete(cm.connections, key)
		cm.mu.Unlock()
	}
	cm.deleteKeyRedis(key)
	return exists
}

func createKey(addr net.Addr, query string) string {
	regexStr := "(?:target|element):\"([a-zA-Z0-9-_*]*)\""
	regex := regexp.MustCompile(regexStr)
	matches := regex.FindAllStringSubmatch(query, -1)
	// connectionKeyString will look like "10.0.0.1|OTHERS|proc|uptime|2017-07-04 00:47:20
	connectionKey := addr.String() + "|"
	for i := 0; i < len(matches); i++ {
		if len(matches[i]) < 2 {
			continue
		}
		connectionKey += matches[i][1] // index 1 contains the value we need
		connectionKey += "|"
	}
	connectionKey += time.Now().UTC().Format(time.RFC3339)
	return connectionKey
}

func (cm *ConnectionManager) storeKeyRedis(key string) {
	if cm.rclient == nil {
		log.V(1).Infof("Redis client is nil, cannot store connection key")
		return
	}
	ret, err := cm.rclient.HSet(table, key, "active").Result()
	if !ret {
		log.V(1).Infof("Subscribe client failed to update telemetry connection key:%s err:%v", key, err)
	}
}

func (cm *ConnectionManager) deleteKeyRedis(key string) {
	if cm.rclient == nil {
		log.V(1).Infof("Redis client is nil, cannot delete connection key")
		return
	}

	ret, err := cm.rclient.HDel(table, key).Result()
	if ret == 0 {
		log.V(1).Infof("Subscribe client failed to delete telemetry connection key:%s err:%v", key, err)
	}
}
