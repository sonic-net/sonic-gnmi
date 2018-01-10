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

	spb "github.com/jipanyang/sonic-telemetry/proto"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

var syncResp = &gpb.SubscribeResponse{
	Response: &gpb.SubscribeResponse_SyncResponse{
		SyncResponse: true,
	},
}

type Value struct {
	*spb.Value
}

// Implement Compare method for priority queue
func (val Value) Compare(other queue.Item) int {
	oval := other.(Value)
	if val.GetTimestamp() > oval.GetTimestamp() {
		return 1
	} else if val.GetTimestamp() == oval.GetTimestamp() {
		return 0
	}
	return -1
}

// Client contains information about a client that has connected to the server.
type Client struct {
	addr      net.Addr
	sendMsg   int64
	recvMsg   int64
	errors    int64
	polled    chan struct{}
	stop      chan struct{}
	mu        sync.RWMutex
	q         *queue.PriorityQueue
	synced    bool
	subscribe *gpb.SubscriptionList
	// mapping from SONiC path to gNMI path
	pathS2G map[string]*gpb.Path
	// target of subscribe request, it is db number in SONiC
	target string
}

// NewClient returns a new initialized client.
func NewClient(addr net.Addr) *Client {
	pq := queue.NewPriorityQueue(1, false)
	return &Client{
		addr:    addr,
		synced:  false,
		q:       pq,
		pathS2G: map[string]*gpb.Path{},
	}
}

// String returns the target the client is querying.
func (c *Client) String() string {
	return c.addr.String()
}

// Run starts the subscribe client. The first message received must be a
// SubscriptionList. Once the client is started, it will run until the stream
// is closed or the schedule completes. For Poll queries the Run will block
// internally after sync until a Poll request is made to the server.
func (c *Client) Run(stream gpb.GNMI_SubscribeServer) (err error) {
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

	log.V(1).Infof("Client %s recieved initial query with go struct : %#v %v", c, query, query)

	c.subscribe = query.GetSubscribe()
	if c.subscribe == nil {
		return grpc.Errorf(codes.InvalidArgument, "first message must be SubscriptionList: %q", query)
	}

	switch mode := c.subscribe.GetMode(); mode {
	case gpb.SubscriptionList_STREAM:
		err = c.populateDbPath(c.subscribe, true)
		if err != nil {
			return grpc.Errorf(codes.InvalidArgument, "Invalid subscription path: %s %q", err, query)
		}
		c.stop = make(chan struct{}, 1)
		// Close of stop channel serves as signal to stop subscribDB routine
		defer close(c.stop)
		go subscribeDb(c)

	case gpb.SubscriptionList_POLL:
		err = c.populateDbPath(c.subscribe, false)
		if err != nil {
			return grpc.Errorf(codes.InvalidArgument, "Invalid subscription path: %s %q", err, query)
		}
		c.polled = make(chan struct{}, 1)
		// Close of polled channel serves as signal to stop pollDb routine
		defer close(c.polled)
		c.polled <- struct{}{}
		go pollDb(c)

	case gpb.SubscriptionList_ONCE:
		return grpc.Errorf(codes.Unimplemented, "SubscriptionList_ONCE is not implemented for SONiC gRPC/gNMI yet: %q", query)
	default:
		return grpc.Errorf(codes.InvalidArgument, "Unkown subscription mode: %q", query)
	}

	log.V(1).Infof("Client %s running", c)
	go c.recv(stream)
	c.send(stream)
	log.V(1).Infof("Client %s shutdown", c)

	return nil
}

// Closing of client queue is triggered upon end of stream receive or stream error
// or fatal error of any client go routine .
// it will cause cancle of client context and exit of the send goroutines.
func (c *Client) Close() {
	if c.q != nil {
		c.q.Dispose()
	}
}

func (c *Client) recv(stream gpb.GNMI_SubscribeServer) {
	for {
		log.V(5).Infof("Client %s blocking on stream.Recv()", c)
		event, err := stream.Recv()
		c.recvMsg++

		switch err {
		default:
			log.V(1).Infof("Client %s received error: %v", c, err)
			c.Close()
			return
		case io.EOF:
			log.V(1).Infof("Client %s received io.EOF", c)
			c.Close()
			return
		case nil:
		}

		if c.subscribe.Mode == gpb.SubscriptionList_POLL {
			log.V(1).Infof("Client %s received Poll event: %v", c, event)
			if _, ok := event.Request.(*gpb.SubscribeRequest_Poll); !ok {
				log.V(1).Infof("Client %s received invalid Poll event: %v", c, event)
				c.Close()
				return
			}
			c.polled <- struct{}{}
			continue
		}
		log.V(1).Infof("Client %s received invalid event: %s", c, event)
	}
	log.V(1).Infof("Client %s exit from recv()", c)
}

// The gNMI worker routines subscribeDb and pollDb push data into the client queue,
// processQueue works as consumber of the queue. The data is popped from queue, converted
// from sonic_gnmi data into a gNMI notification, then sent on stream.
func (c *Client) processQueue(stream gpb.GNMI_SubscribeServer) error {
	for {
		items, err := c.q.Get(1)

		if items == nil {
			return fmt.Errorf("queue closed %v", err)
		}
		if err != nil {
			c.errors++
			return fmt.Errorf("unexpected queue Gext(1): %v", err)
		}

		var resp *gpb.SubscribeResponse
		switch v := items[0].(type) {
		case Value:
			if resp, err = c.valToResp(v); err != nil {
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
			log.V(1).Infof("Client %s sending error:%v", c, resp)
			c.errors++
			return err
		}
		log.V(2).Infof("Client %s done sending, msg count %d, msg %v", c, c.sendMsg, resp)
	}
}

// send runs until process Queue returns an error.
func (c *Client) send(stream gpb.GNMI_SubscribeServer) {
	for {
		if err := c.processQueue(stream); err != nil {
			log.Errorf("Client %s error: %v", c, err)
			return
		}
	}
}

func getGnmiPathPrefix(c *Client) (*gpb.Path, error) {
	sublist := c.subscribe
	if sublist == nil {
		return nil, fmt.Errorf("No SubscriptionList")
	}
	prefix := sublist.GetPrefix()
	log.V(6).Infof("prefix : %#v SubscribRequest : %#v", sublist)

	return prefix, nil
}

// Convert from SONiC Value to its corresponding gNMI proto stream
// response type.
func (c *Client) valToResp(val Value) (*gpb.SubscribeResponse, error) {
	switch val.GetSyncResponse() {
	case true:
		return &gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_SyncResponse{
				SyncResponse: true,
			},
		}, nil
	default:
		gnmiPath, ok := c.pathS2G[val.GetPath()]
		if !ok {
			return nil, fmt.Errorf("Failed to find gNMI path for %v", val.GetPath())
		}
		prefix, err := getGnmiPathPrefix(c)
		if err != nil {
			return nil, err
		}
		return &gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_Update{
				Update: &gpb.Notification{
					Timestamp: val.GetTimestamp(),
					Prefix:    prefix,
					Update: []*gpb.Update{
						{
							Path: gnmiPath,
							Val:  val.GetVal(),
						},
					},
				},
			},
		}, nil
	}
}
