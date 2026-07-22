// remote_observation_app.go 组装 API 与安全远程观察模块的内部 Host 边界。
//
// 职责：
//   - 把 remote.Store 适配为 remoteobservation.HostSource
//   - 只传递 host ID、PublicIP 与 PrivateIP 三个探测所需字段
//
// 边界：
//   - 不传递 Host.Name、SSHHost 或任何 SSH 凭据
//   - 不执行网络探测，探测策略属于 remoteobservation 模块
package api

import (
	"context"

	"github.com/xsxdot/super-dev/agent/remote"
	"github.com/xsxdot/super-dev/agent/remoteobservation"
)

type remoteObservationHostSource struct {
	store *remote.Store
}

// ObservationHost 从 Host 存储投影探测模块所需的最小地址事实。
func (s remoteObservationHostSource) ObservationHost(_ context.Context, hostID string) (remoteobservation.HostAddressFacts, bool, error) {
	if s.store == nil {
		return remoteobservation.HostAddressFacts{}, false, nil
	}
	hosts, err := s.store.ListHosts()
	if err != nil {
		return remoteobservation.HostAddressFacts{}, false, err
	}
	for _, host := range hosts {
		if host.ID != hostID {
			continue
		}
		return remoteobservation.HostAddressFacts{
			HostID: host.ID, PublicIP: host.PublicIP, PrivateIP: host.PrivateIP,
		}, true, nil
	}
	return remoteobservation.HostAddressFacts{}, false, nil
}
