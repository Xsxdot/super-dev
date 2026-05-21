// controller.go 实现通过隧道控制远端 collector 的能力。
//
// 职责：
//   - 根据 hostID 通过 TunnelResolver 获取本地隧道 baseURL
//   - 调用远端 /api/collectors 启停采集任务
//   - 解析响应,返回稳定 collector ID
//
// 边界：
//   - 不管理隧道生命周期,由 tunnel.Manager 负责
//   - 不持久化"本机视角的 collector 映射",每次 EnsureCollector 都调远端
//     (远端通过 hash(name+type) 保证 ID 稳定 → 幂等)
package remote

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/superdev/agent/model"
)

// ErrHostUnreachable 表示无法获得 host 对应的本地隧道 baseURL。
var ErrHostUnreachable = errors.New("host unreachable")

// TunnelResolver 把 hostID 解析为本地隧道 baseURL(如 "http://127.0.0.1:12345")。
//
// 生产实现由 api 层适配 tunnel.Manager,测试用 fakeTunnel 注入。
type TunnelResolver interface {
	BaseURL(hostID string) (string, error)
}

// Controller 提供本机端的远端 collector 控制能力。
type Controller struct {
	store   *Store
	tunnels TunnelResolver
	httpDo  *http.Client
}

// NewController 创建 Controller。
//
// 参数：
//   - store: 用于查询 Host/LogSource(校验 host 属于 LogSource.HostIDs)
//   - tunnels: 隧道解析器(生产用 api 层 adapter,测试用 fakeTunnel)
//   - httpDo: HTTP 客户端,通常 http.DefaultClient;测试可注入自定义超时
func NewController(store *Store, tunnels TunnelResolver, httpDo *http.Client) *Controller {
	if httpDo == nil {
		httpDo = http.DefaultClient
	}
	return &Controller{store: store, tunnels: tunnels, httpDo: httpDo}
}

// EnsureCollector 在 hostID 上启动 logSourceID 对应的远端采集任务。
//
// 参数：
//   - hostID, logSourceID: 两端的 ID
//
// 返回：
//   - 远端的 collector ID(同一 name+type 始终相同 → 幂等)
//   - hostID 无隧道时返回 ErrHostUnreachable
//   - 远端返回非 2xx 时返回错误
func (c *Controller) EnsureCollector(hostID, logSourceID string) (string, error) {
	ls, err := c.findLogSourceForHost(hostID, logSourceID)
	if err != nil {
		return "", err
	}
	base, err := c.tunnels.BaseURL(hostID)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]string{"name": ls.Name, "type": string(ls.Type)})
	if err != nil {
		return "", err
	}
	resp, err := c.httpDo.Post(joinURL(base, "/api/collectors"), "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	var col model.Collector
	if err := json.NewDecoder(resp.Body).Decode(&col); err != nil {
		return "", err
	}
	return col.ID, nil
}

// StopCollector 停止 hostID 上 logSourceID 对应的远端采集任务。
func (c *Controller) StopCollector(hostID, logSourceID string) error {
	ls, err := c.findLogSourceForHost(hostID, logSourceID)
	if err != nil {
		return err
	}
	base, err := c.tunnels.BaseURL(hostID)
	if err != nil {
		return err
	}
	id, err := c.findRemoteCollectorID(base, ls.Name, ls.Type)
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	req, err := http.NewRequest(http.MethodDelete, joinURL(base, "/api/collectors/"+id), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpDo.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	return nil
}

// ListRemoteCollectors 通过隧道查询 hostID 上当前活跃的 collector。
//
// 主要用途:本机重连后对账,或 UI 调试。
func (c *Controller) ListRemoteCollectors(hostID string) ([]model.Collector, error) {
	base, err := c.tunnels.BaseURL(hostID)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpDo.Get(joinURL(base, "/api/collectors"))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	var list []model.Collector
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []model.Collector{}
	}
	return list, nil
}

func (c *Controller) findLogSourceForHost(hostID, logSourceID string) (model.LogSource, error) {
	if _, err := c.findHost(hostID); err != nil {
		return model.LogSource{}, err
	}
	list, err := c.store.ListLogSources()
	if err != nil {
		return model.LogSource{}, err
	}
	for _, ls := range list {
		if ls.ID != logSourceID {
			continue
		}
		for _, id := range ls.HostIDs {
			if id == hostID {
				return ls, nil
			}
		}
		return model.LogSource{}, ErrNotFound
	}
	return model.LogSource{}, ErrNotFound
}

func (c *Controller) findHost(id string) (model.Host, error) {
	hosts, err := c.store.ListHosts()
	if err != nil {
		return model.Host{}, err
	}
	for _, h := range hosts {
		if h.ID == id {
			return h, nil
		}
	}
	return model.Host{}, ErrNotFound
}

// findRemoteCollectorID 在远端 List 中找到 (name, type) 对应的 collector_id。
func (c *Controller) findRemoteCollectorID(base, name string, t model.LogSourceType) (string, error) {
	resp, err := c.httpDo.Get(joinURL(base, "/api/collectors"))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("remote returned %d: %s", resp.StatusCode, msg)
	}
	var list []model.Collector
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return "", err
	}
	for _, col := range list {
		if col.Name == name && col.Type == t {
			return col.ID, nil
		}
	}
	return "", nil
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}
