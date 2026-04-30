package client

import (
	"testing"

	"github.com/Azure/sonic-mgmt-common/translib"
	"github.com/Workiva/go-datastructures/queue"
	spb "github.com/openconfig/gnmi/proto/gnmi"
)

func TestNotify(t *testing.T) {
	tests := []struct {
		name        string
		v           *translib.SubscribeResponse
		builderMsg  *spb.Notification
		builderErr  error
		hasSuperSub bool
		expectedErr bool
		expectedLen int
	}{
		{
			name: "Standard notification path",
			v:    &translib.SubscribeResponse{},
			builderMsg: &spb.Notification{
				Update: []*spb.Update{{Path: &spb.Path{Target: "test"}}},
			},
			expectedErr: false,
			expectedLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. Initialize the Subscriber and Client pointers
			ts := &translSubscriber{}
			ts.client = &TranslClient{}

			// 2. Fix the Queue Assignment
			ts.client.q = queue.NewPriorityQueue(10, false)

			// 3. Mock the msgBuilder function
			ts.msgBuilder = func(resp *translib.SubscribeResponse, s *translSubscriber) (*spb.Notification, error) {
				return tt.builderMsg, tt.builderErr
			}

			// 4. Handle SuperSub if needed
			if tt.hasSuperSub {
				ts.client.superSub = &superSubscription{}
			}

			// Execute
			ts.notify(tt.v)
		})
	}
}
func TestNotifyNil(t *testing.T) {
	tests := []struct {
		name        string
		v           *translib.SubscribeResponse
		builderMsg  *spb.Notification
		builderErr  error
		hasSuperSub bool
		expectedErr bool
		expectedLen int
	}{
		{
			name: "Standard notification path",
			v:    &translib.SubscribeResponse{},
			builderMsg: &spb.Notification{
				Update: []*spb.Update{{Path: &spb.Path{Target: "test"}}},
			},
			expectedErr: false,
			expectedLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 1. Initialize the Subscriber and Client pointers
			ts := &translSubscriber{}
			ts.client = &TranslClient{}

			// 2. Fix the Queue Assignment
			ts.client.q = queue.NewPriorityQueue(10, false)

			// 3. Mock the msgBuilder function
			ts.msgBuilder = func(resp *translib.SubscribeResponse, s *translSubscriber) (*spb.Notification, error) {
				if resp == nil {
					// Return a valid notification to pass the (len == 0) check
					return &spb.Notification{
						Update: []*spb.Update{{}},
					}, nil
				}
				return nil, nil
			}

			// 4. Handle SuperSub if needed
			if tt.hasSuperSub {
				ts.client.superSub = &superSubscription{}
			}

			// Execute
			ts.notify(tt.v)
		})
	}
}
