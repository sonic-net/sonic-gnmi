package common_utils

import (
	"encoding/json"

	"github.com/go-redis/redis"
	log "github.com/golang/glog"
)

// NotificationProducer provides utilities for sending messages using notification channel.
// NewNotificationProducer must be called for a new producer.
// Close must be called when finished.
type NotificationProducer struct {
	ch string
	rc *redis.Client
}

// NewNotificationProducer returns a new NotificationProducer.
func NewNotificationProducer(ch string) (*NotificationProducer, error) {
	n := new(NotificationProducer)
	n.ch = ch

	// Create redis client.
	var err error
	n.rc, err = getRedisDBClient()
	if err != nil {
		return nil, err
	}

	return n, nil
}

// Close performs cleanup works.
// Close must be called when finished.
func (n *NotificationProducer) Close() {
	if n.rc != nil {
		n.rc.Close()
	}
}

func (n *NotificationProducer) Send(op, data string, kvs map[string]string) error {
	fvs := []string{op, data}
	for k, v := range kvs {
		fvs = append(fvs, k)
		fvs = append(fvs, v)
	}

	val, err := json.Marshal(fvs)
	if err != nil {
		log.Error(err.Error())
		return err
	}
	log.Infof("Publishing to channel %s: %v.", n.ch, string(val))
	return n.rc.Publish(n.ch, val).Err()
}