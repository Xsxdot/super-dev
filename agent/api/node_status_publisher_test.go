// node_status_publisher_test.go 验证远端节点状态 publisher 的推送时机。
//
// 职责：
//   - 覆盖订阅后立即发送节点快照
//   - 覆盖 managed 状态变化信号触发即时推送
//
// 边界：
//   - 不建立真实 WebSocket 连接
//   - 不测试桌面端 NodeRegistry 消费逻辑
package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNodeStatusPublisherPushesImmediatelyOnSignal(t *testing.T) {
	app, err := NewApp(AppConfig{DataDir: t.TempDir()})
	require.NoError(t, err)
	defer app.Close()

	pub := newNodeStatusPublisher(app, "h1", "ali-01", time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := pub.Subscribe(ctx)

	initial := <-ch
	require.Len(t, initial, 1)
	require.Equal(t, "h1", initial[0].HostID)

	pub.Signal()

	require.Eventually(t, func() bool {
		select {
		case batch := <-ch:
			return len(batch) == 1 && batch[0].HostID == "h1"
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}
