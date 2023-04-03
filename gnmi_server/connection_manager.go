package gnmi

import (
	"sync"
	"time"
	"net"
	"regexp"
	log "github.com/golang/glog"
)

type ConnectionManager struct {
	connections  map[string]struct{}
	mu           sync.Mutex
	threshold    int
}

func (cm *ConnectionManager) Count() int {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return len(cm.connections)
}

func (cm *ConnectionManager) Add(addr net.Addr, query string) (string, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if len(cm.connections) >= cm.threshold {
		log.V(1).Infof("Cannot add another client connection as threshold is already at limit")
		return "", false
	}
	key := createKey(addr, query)
	log.V(1).Infof("Adding client connection: %s", key)
	cm.connections[key] = struct{}{}
	log.V(1).Infof("Current number of existing connections: %d", len(cm.connections))
	return key, true
}

func (cm *ConnectionManager) Remove(key string) (bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	_, exists := cm.connections[key]
	if exists {
		log.V(1).Infof("Closing connection: %s", key)
		delete(cm.connections, key)
	}
	return exists
}

func createKey(addr net.Addr, query string) string {
	regexStr := "(?:target|element):\"([a-zA-Z0-9-_]*)\""
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
