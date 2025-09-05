package gnmi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/golang/glog"
	"github.com/openconfig/gnmi/proto/gnmi"
)

// DiskSpaceInfo represents disk space information returned by the server.
type DiskSpaceInfo struct {
	Path        string `json:"path"`
	TotalMB     int64  `json:"total-mb"`
	AvailableMB int64  `json:"available-mb"`
}

// GetDiskSpace retrieves disk space information for the specified filesystem path.
// This is a convenience method that constructs the appropriate gNMI path and
// handles the response parsing.
func (c *Client) GetDiskSpace(ctx context.Context, filesystemPath string) (*DiskSpaceInfo, error) {
	if filesystemPath == "" {
		return nil, fmt.Errorf("filesystem path is required")
	}

	// Construct the gNMI path: /sonic/system/filesystem[path=<filesystemPath>]/disk-space
	path := &gnmi.Path{
		Elem: []*gnmi.PathElem{
			{Name: "sonic"},
			{Name: "system"},
			{
				Name: "filesystem",
				Key:  map[string]string{"path": filesystemPath},
			},
			{Name: "disk-space"},
		},
	}

	glog.V(2).Infof("Requesting disk space for filesystem path: %s", filesystemPath)

	// Make the gNMI Get request
	resp, err := c.Get(ctx, []*gnmi.Path{path}, gnmi.Encoding_JSON)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk space for path %s: %w", filesystemPath, err)
	}

	// Parse the response
	if len(resp.Notification) == 0 || len(resp.Notification[0].Update) == 0 {
		return nil, fmt.Errorf("no data received for path %s", filesystemPath)
	}

	update := resp.Notification[0].Update[0]
	jsonVal := update.Val.GetJsonVal()
	if jsonVal == nil {
		return nil, fmt.Errorf("expected JSON response, got %T", update.Val.Value)
	}

	// Unmarshal the JSON response
	var diskInfo DiskSpaceInfo
	if err := json.Unmarshal(jsonVal, &diskInfo); err != nil {
		return nil, fmt.Errorf("failed to parse disk space response: %w", err)
	}

	glog.V(2).Infof("Retrieved disk space for %s: %d MB total, %d MB available",
		filesystemPath, diskInfo.TotalMB, diskInfo.AvailableMB)

	return &diskInfo, nil
}
