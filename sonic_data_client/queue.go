package client

import (
	"sync"
	"time"

	log "github.com/golang/glog"

	"github.com/Workiva/go-datastructures/queue"
	spb "github.com/sonic-net/sonic-gnmi/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type LimitedQueue struct {
	Q               *queue.PriorityQueue
	queueLengthLock sync.Mutex
	queueLengthSum  uint64
	maxSize         uint64
}

func (q *LimitedQueue) EnqueueItem(item Value) error {
	q.queueLengthLock.Lock()
	defer q.queueLengthLock.Unlock()
	ilen := GetValueSize(item)
	if ilen+q.queueLengthSum < q.maxSize {
		q.queueLengthSum += ilen
		log.V(2).Infof("Output queue size: %d", q.queueLengthSum)
		return q.Q.Put(item)
	} else {
		log.Error("Telemetry output queue full, closing subscription!")
		return status.Error(codes.ResourceExhausted, "Subscribe output queue exhausted")
	}
}

func GetValueSize(item Value) uint64 {
	if item.Notification != nil {
		return (uint64)(proto.Size(item.Notification))
	}
	return (uint64)(proto.Size(item.Val))
}

func (q *LimitedQueue) ForceEnqueueItem(item Value) error {
	q.queueLengthLock.Lock()
	defer q.queueLengthLock.Unlock()
	q.queueLengthSum += GetValueSize(item)
	log.V(2).Infof("Output queue size: %d", q.queueLengthSum)
	return q.Q.Put(item)
}

func (q *LimitedQueue) DequeueItem() (Value, error) {
	items, err := q.Q.Get(1)
	if err != nil {
		return Value{}, err
	}
	ilen := (uint64)(proto.Size(items[0].(Value).Val))
	if items[0].(Value).Notification != nil {
		ilen = (uint64)(proto.Size(items[0].(Value).Notification))
	}
	q.queueLengthLock.Lock()
	defer q.queueLengthLock.Unlock()
	q.queueLengthSum -= ilen
	log.V(2).Infof("Output queue size: %d", q.queueLengthSum)
	return items[0].(Value), nil
}

func NewLimitedQueue(hint int, allowDuplicates bool, maxSize uint64) *LimitedQueue {
	return &LimitedQueue{
		Q:       queue.NewPriorityQueue(hint, allowDuplicates),
		maxSize: maxSize,
	}
}

func (q *LimitedQueue) enqueFatalMsg(msg string) {
	log.ErrorDepth(1, msg)
	q.ForceEnqueueItem(Value{
		&spb.Value{
			Timestamp: time.Now().UnixNano(),
			Fatal:     msg,
		},
	})
}
