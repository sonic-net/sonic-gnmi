package gnmi

import (
	"fmt"
	"io"
	"net"
	"sync"
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

func (cm *ConnectionManager) Add(key string) (success bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if len(cm.connections) >= threshold {
		log.V(1).Infof("Cannot add another client connection as threshold is already at limit")
		return false
	}
	log.V(1).Infof("Adding client connection: %s", key)
	cm.connections[key] = struct{}
	log.V(1).Infof("Current number of existing connections: %d", len(cm.connections)
	return true
}

func (cm *ConnectionManager) Remove(key string) (success bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	_, exists := cm.connections[key]; exists {
		log.V(1).Infof("Closing connection: %s", key)
		delete(cm.connections, key)
	}
	return exists
}
