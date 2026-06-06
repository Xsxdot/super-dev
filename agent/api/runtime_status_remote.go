package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// RuntimeStatusClient 从指定远端 host 读取 runtime-status 快照。
type RuntimeStatusClient interface {
	Fetch(ctx context.Context, hostID, projectID string) (model.RuntimeStatusResponse, error)
}

type transportRuntimeStatusClient struct {
	transport nodetransport.NodeTransport
}

func newTransportRuntimeStatusClient(transport nodetransport.NodeTransport) *transportRuntimeStatusClient {
	return &transportRuntimeStatusClient{transport: transport}
}

// Fetch 通过节点传输请求远端 agent 的 runtime-status 接口。
func (c *transportRuntimeStatusClient) Fetch(ctx context.Context, hostID, projectID string) (model.RuntimeStatusResponse, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	resp, err := c.transport.Do(reqCtx, hostID, nodetransport.NodeRequest{
		Method: http.MethodGet,
		Path:   "/api/projects/" + url.PathEscape(projectID) + "/runtime-status",
	})
	if err != nil {
		return model.RuntimeStatusResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return model.RuntimeStatusResponse{}, fmt.Errorf("remote runtime-status returned %d", resp.StatusCode)
	}
	var payload model.RuntimeStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return model.RuntimeStatusResponse{}, err
	}
	return payload, nil
}
