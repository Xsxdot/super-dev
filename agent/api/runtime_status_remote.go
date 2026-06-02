package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/superdev/agent/logbackend"
	"github.com/superdev/agent/model"
)

// RuntimeStatusClient 从指定远端 host 读取 runtime-status 快照。
type RuntimeStatusClient interface {
	Fetch(ctx context.Context, hostID, projectID string) (model.RuntimeStatusResponse, error)
}

type tunnelRuntimeStatusClient struct {
	resolver logbackend.TunnelResolver
}

func newTunnelRuntimeStatusClient(resolver logbackend.TunnelResolver) *tunnelRuntimeStatusClient {
	return &tunnelRuntimeStatusClient{resolver: resolver}
}

// Fetch 通过已建立的 SSH 隧道请求远端 agent 的 runtime-status 接口。
func (c *tunnelRuntimeStatusClient) Fetch(ctx context.Context, hostID, projectID string) (model.RuntimeStatusResponse, error) {
	base, err := c.resolver.BaseURL(hostID)
	if err != nil {
		return model.RuntimeStatusResponse{}, err
	}
	if base == "" {
		return model.RuntimeStatusResponse{}, fmt.Errorf("tunnel not connected for host %s", hostID)
	}
	u, err := url.Parse(base + "/api/projects/" + url.PathEscape(projectID) + "/runtime-status")
	if err != nil {
		return model.RuntimeStatusResponse{}, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return model.RuntimeStatusResponse{}, err
	}
	resp, err := http.DefaultClient.Do(req)
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
