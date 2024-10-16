package common_utils

import (
	"testing"
)

const (
	channel string = "channel"
)

func TestNotificationProducerSucceedsWithEmptyOp(t *testing.T) {
	n, _ := NewNotificationProducer(channel)
	defer n.Close()
	if err := n.Send("", "somedata", map[string]string{}); err != nil {
		t.Fatalf("Expected no error!")
	}
}

func TestNotificationProducerSucceedsWithEmptyData(t *testing.T) {
	n, _ := NewNotificationProducer(channel)
	defer n.Close()
	if err := n.Send("someop", "", map[string]string{}); err != nil {
		t.Fatalf("Expected no error!")
	}
}

func TestNotificationProducerSucceedsWithEmptyOpAndData(t *testing.T) {
	n, _ := NewNotificationProducer(channel)
	defer n.Close()
	if err := n.Send("", "", map[string]string{}); err != nil {
		t.Fatalf("Expected no error!")
	}
}

func TestNotificationProducerSucceedsWithEmptyKeyValues(t *testing.T) {
	n, _ := NewNotificationProducer(channel)
	defer n.Close()
	if err := n.Send("someop", "somedata", map[string]string{}); err != nil {
		t.Fatalf("Expected no error!")
	}
}

func TestNotificationProducerSucceeds(t *testing.T) {
	n, _ := NewNotificationProducer(channel)
	defer n.Close()
	if err := n.Send("someop", "somedata", map[string]string{"somekey": "somevalue"}); err != nil {
		t.Fatalf("Expected no error!")
	}
}
