package common_utils

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sonic-net/sonic-gnmi/localredis"
	sdcfg "github.com/sonic-net/sonic-gnmi/sonic_db_config"

	log "github.com/golang/glog"
	"github.com/redis/go-redis/v9"
)

const (
	dbName = "STATE_DB"
)

func GetRedisDBClient() (*redis.Client, error) {
	ns, _ := sdcfg.GetDbDefaultNamespace()
	opts, err := localredis.Options(dbName, ns)
	if err != nil {
		log.Errorf("Redis endpoint err: %v", err)
		return nil, err
	}
	rclient := redis.NewClient(opts)
	if _, err := rclient.Ping(context.Background()).Result(); err != nil {
		return nil, err
	}
	return rclient, nil
}

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
	n.rc, err = GetRedisDBClient()
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
	return n.rc.Publish(context.Background(), n.ch, val).Err()
}

// getKey generates the hash key from the supplied string array.
func GetKey(k []string) string {
	return strings.Join(k, "|")
}
