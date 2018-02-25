package gnmi

import (
	"fmt"
	"io"
	"net"
	"sync"

	log "github.com/golang/glog"
	"github.com/workiva/go-datastructures/queue"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	//spb "github.com/jipanyang/sonic-telemetry/proto"
	sdc "github.com/jipanyang/sonic-telemetry/sonic_data_client"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
)

// Client contains information about a subscribe client that has connected to the server.
type Client struct {
	addr      net.Addr
	sendMsg   int64
	recvMsg   int64
	errors    int64
	polled    chan struct{}
	stop      chan struct{}
	mu        sync.RWMutex
	q         *queue.PriorityQueue
	subscribe *gnmipb.SubscriptionList
	// Wait for all sub go routine to finish
	w     sync.WaitGroup
	fatal bool
}

// NewClient returns a new initialized client.
func NewClient(addr net.Addr) *Client {
	pq := queue.NewPriorityQueue(1, false)
	return &Client{
		addr: addr,
		q:    pq,
	}
}

// String returns the target the client is querying.
func (c *Client) String() string {
	return c.addr.String()
}

// Populate SONiC data path from prefix and subscription path.
func (c *Client) populateDbPathSubscrition(sublist *gnmipb.SubscriptionList) ([]*gnmipb.Path, error) {
	var paths []*gnmipb.Path

	prefix := sublist.GetPrefix()
	log.V(6).Infof("prefix : %#v SubscribRequest : %#v", prefix, sublist)

	subscriptions := sublist.GetSubscription()
	if subscriptions == nil {
		return nil, fmt.Errorf("No Subscription")
	}

	for _, subscription := range subscriptions {
		path := subscription.GetPath()
		paths = append(paths, path)
	}

	log.V(6).Infof("gnmi Paths : %v", paths)
	return paths, nil
}

// Run starts the subscribe client. The first message received must be a
// SubscriptionList. Once the client is started, it will run until the stream
// is closed or the schedule completes. For Poll queries the Run will block
// internally after sync until a Poll request is made to the server.
func (c *Client) Run(stream gnmipb.GNMI_SubscribeServer) (err error) {
	defer log.V(1).Infof("Client %s shutdown", c)

	if stream == nil {
		return grpc.Errorf(codes.FailedPrecondition, "cannot start client: stream is nil")
	}

	defer func() {
		if err != nil {
			c.errors++
		}
	}()

	query, err := stream.Recv()
	c.recvMsg++
	if err != nil {
		if err == io.EOF {
			return grpc.Errorf(codes.Aborted, "stream EOF received before init")
		}
		return grpc.Errorf(grpc.Code(err), "received error from client")
	}

	log.V(2).Infof("Client %s recieved initial query %v", c, query)

	c.subscribe = query.GetSubscribe()
	if c.subscribe == nil {
		return grpc.Errorf(codes.InvalidArgument, "first message must be SubscriptionList: %q", query)
	}

	var target string
	prefix := c.subscribe.GetPrefix()
	if prefix == nil {
		return grpc.Errorf(codes.Unimplemented, "No target specified in prefix")
	} else {
		target = prefix.GetTarget()
		// TODO: add data client support for fetching non-db data
		if target == "" {
			return grpc.Errorf(codes.Unimplemented, "Empty target data not supported yet")
		}
	}

	paths, err := c.populateDbPathSubscrition(c.subscribe)
	if err != nil {
		return grpc.Errorf(codes.NotFound, "Invalid subscription path: %v %q", err, query)
	}
	var dc sdc.Client
	if target == "OTHERS" {
		dc, err = sdc.NewNonDbClient(paths, prefix)
	} else {
		dc, err = sdc.NewDbClient(paths, prefix)
	}

	if err != nil {
		return grpc.Errorf(codes.NotFound, "%v", err)
	}

	switch mode := c.subscribe.GetMode(); mode {
	case gnmipb.SubscriptionList_STREAM:
		c.stop = make(chan struct{}, 1)
		c.w.Add(1)
		go dc.StreamRun(c.q, c.stop, &c.w)
	case gnmipb.SubscriptionList_POLL:
		c.polled = make(chan struct{}, 1)
		c.polled <- struct{}{}
		c.w.Add(1)
		go dc.PollRun(c.q, c.polled, &c.w)
	case gnmipb.SubscriptionList_ONCE:
		return grpc.Errorf(codes.Unimplemented, "SubscriptionList_ONCE is not implemented for SONiC gRPC/gNMI yet: %q", query)
	default:
		return grpc.Errorf(codes.InvalidArgument, "Unkown subscription mode: %q", query)
	}

	log.V(1).Infof("Client %s running", c)
	go c.recv(stream)
	err = c.send(stream)
	c.Close()
	// Wait until all child go routines exited
	c.w.Wait()
	return grpc.Errorf(codes.InvalidArgument, "%s", err)
}

// Closing of client queue is triggered upon end of stream receive or stream error
// or fatal error of any client go routine .
// it will cause cancle of client context and exit of the send goroutines.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	log.V(1).Infof("Client %s Close, sendMsg %v recvMsg %v errors %v", c, c.sendMsg, c.recvMsg, c.errors)
	if c.q != nil {
		if c.q.Disposed() {
			return
		}
		c.q.Dispose()
	}
	if c.stop != nil {
		close(c.stop)
	}
	if c.polled != nil {
		close(c.polled)
	}
}

func (c *Client) recv(stream gnmipb.GNMI_SubscribeServer) {
	defer c.Close()

	for {
		log.V(5).Infof("Client %s blocking on stream.Recv()", c)
		event, err := stream.Recv()
		c.recvMsg++

		switch err {
		default:
			log.V(1).Infof("Client %s received error: %v", c, err)
			return
		case io.EOF:
			log.V(1).Infof("Client %s received io.EOF", c)
			return
		case nil:
		}

		if c.subscribe.Mode == gnmipb.SubscriptionList_POLL {
			log.V(3).Infof("Client %s received Poll event: %v", c, event)
			if _, ok := event.Request.(*gnmipb.SubscribeRequest_Poll); !ok {
				return
			}
			c.polled <- struct{}{}
			continue
		}
		log.V(1).Infof("Client %s received invalid event: %s", c, event)
	}
	log.V(1).Infof("Client %s exit from recv()", c)
}

// send runs until process Queue returns an error.
func (c *Client) send(stream gnmipb.GNMI_SubscribeServer) error {
	for {
		items, err := c.q.Get(1)

		if items == nil {
			log.V(1).Infof("%v", err)
			return err
		}
		if err != nil {
			c.errors++
			log.V(1).Infof("%v", err)
			return fmt.Errorf("unexpected queue Gext(1): %v", err)
		}

		var resp *gnmipb.SubscribeResponse
		switch v := items[0].(type) {
		case sdc.Value:
			if resp, err = sdc.ValToResp(v); err != nil {
				c.errors++
				return err
			}
		default:
			log.V(1).Infof("Unknown data type %v for %s in queue", items[0], c)
			c.errors++
		}

		c.sendMsg++
		err = stream.Send(resp)
		if err != nil {
			log.V(1).Infof("Client %s sending error:%v", c, err)
			c.errors++
			return err
		}
		log.V(5).Infof("Client %s done sending, msg count %d, msg %v", c, c.sendMsg, resp)
	}
}
