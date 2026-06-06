// Package api 中的 run_hub_test.go 验证 pipeline run 实时事件总线。
//
// 职责：
//   - 验证 run 事件可以按订阅广播
//   - 验证取消订阅只影响当前订阅者
//   - 验证慢消费者会被关闭，避免阻塞广播方
//
// 边界：
//   - 不启动 HTTP 或 WebSocket 服务
//   - 不验证日志持久化和 pipeline 执行流程
package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunHubBroadcastAndClose(t *testing.T) {
	hub := NewRunHub()
	sub, cancel := hub.Subscribe("run-1")
	defer cancel()

	hub.Broadcast("run-1", RunEvent{Kind: RunEventKindStatus, Status: &RunStatusPatch{StepName: "Deploy"}})

	select {
	case got := <-sub.Ch():
		require.NotNil(t, got.Status)
		assert.Equal(t, RunEventKindStatus, got.Kind)
		assert.Equal(t, "Deploy", got.Status.StepName)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for run event")
	}

	hub.Close("run-1")
	_, ok := <-sub.Ch()
	assert.False(t, ok)
}

func TestRunHubUnsubscribeRemovesOnlyThatSubscriber(t *testing.T) {
	hub := NewRunHub()
	first, cancelFirst := hub.Subscribe("run-1")
	second, cancelSecond := hub.Subscribe("run-1")
	defer cancelSecond()

	cancelFirst()
	hub.Broadcast("run-1", RunEvent{Kind: RunEventKindDone})

	select {
	case _, ok := <-first.Ch():
		assert.False(t, ok)
	default:
		t.Fatal("first subscriber channel was not closed")
	}

	select {
	case got := <-second.Ch():
		assert.Equal(t, RunEventKindDone, got.Kind)
	case <-time.After(time.Second):
		t.Fatal("second subscriber did not receive event")
	}
}

func TestRunHubDropsSlowSubscriber(t *testing.T) {
	hub := newRunHubWithBuffer(1)
	sub, cancel := hub.Subscribe("run-1")
	defer cancel()

	hub.Broadcast("run-1", RunEvent{Kind: RunEventKindStatus, Status: &RunStatusPatch{StepName: "one"}})
	hub.Broadcast("run-1", RunEvent{Kind: RunEventKindStatus, Status: &RunStatusPatch{StepName: "two"}})

	first := <-sub.Ch()
	assert.Equal(t, "one", first.Status.StepName)
	_, ok := <-sub.Ch()
	assert.False(t, ok)
}
