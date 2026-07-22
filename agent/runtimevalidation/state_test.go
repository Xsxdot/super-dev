// state_test.go 验证固定 active marker 与 append-only cleanup journal 的 fail-closed 合同。
//
// 职责：锁定旧 marker 阻断、intent/acquired/released 顺序和 fsync 后快照。
// 边界：不自动恢复旧 campaign，也不在 journal 中记录 secret。
package runtimevalidation

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActiveMarkerBlocksNextCampaignUntilExplicitRemoval(t *testing.T) {
	t.Parallel()

	stateRoot := t.TempDir()
	marker := ActiveMarker{CampaignID: "campaign-old", BundleDigest: "abc", ClonePath: "/tmp/clone", RemoteRoot: "/srv/old"}
	require.NoError(t, WriteActiveMarker(stateRoot, marker))

	loaded, err := ReadActiveMarker(stateRoot)
	require.NoError(t, err)
	require.Equal(t, marker.CampaignID, loaded.CampaignID)
	result, err := CheckActiveMarker(stateRoot)
	require.NoError(t, err)
	require.Equal(t, StatusBlocked, result.Status)

	require.NoError(t, RemoveActiveMarker(stateRoot))
	result, err = CheckActiveMarker(stateRoot)
	require.NoError(t, err)
	require.Equal(t, StatusPass, result.Status)
}

func TestCleanupJournalRequiresIntentAcquireReleaseAndRejectsSecrets(t *testing.T) {
	t.Parallel()

	journal, err := OpenCleanupJournal(filepath.Join(t.TempDir(), "cleanup-journal.jsonl"), "campaign-1", func() time.Time {
		return time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	})
	require.NoError(t, err)
	defer journal.Close()

	require.NoError(t, journal.Intent("service", "svc-1", "campaign-1", map[string]any{"state": "stopped"}))
	require.NoError(t, journal.Acquired("service", "svc-1", "campaign-1"))
	snapshot := journal.Snapshot()
	require.False(t, snapshot.Complete)
	require.Len(t, snapshot.Unreleased, 1)
	require.NoError(t, journal.Released("service", "svc-1", "campaign-1"))
	require.True(t, journal.Snapshot().Complete)

	require.Error(t, journal.Intent("credential", "lease-1", "campaign-1", map[string]any{"token": "must-not-persist"}))
}
