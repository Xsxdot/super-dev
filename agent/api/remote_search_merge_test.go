package api_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/superdev/agent/api"
	"github.com/superdev/agent/model"
)

func mergeEntry(host string, ts time.Time, id int64, msg string) api.MergeItem {
	return api.MergeItem{HostID: host, Entry: model.LogEntry{ID: id, Timestamp: ts, Message: msg}}
}

func TestMergeStreamsBasic(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	streams := map[string][]model.LogEntry{
		"h1": {
			{ID: 1, Timestamp: now.Add(0), Message: "a1"},
			{ID: 3, Timestamp: now.Add(2 * time.Second), Message: "a3"},
		},
		"h2": {
			{ID: 2, Timestamp: now.Add(time.Second), Message: "b2"},
			{ID: 4, Timestamp: now.Add(3 * time.Second), Message: "b4"},
		},
	}

	out := api.MergeStreams(streams, 10)

	require.Len(t, out, 4)
	assert.Equal(t, mergeEntry("h1", now, 1, "a1"), out[0])
	assert.Equal(t, mergeEntry("h2", now.Add(time.Second), 2, "b2"), out[1])
	assert.Equal(t, mergeEntry("h1", now.Add(2*time.Second), 3, "a3"), out[2])
	assert.Equal(t, mergeEntry("h2", now.Add(3*time.Second), 4, "b4"), out[3])
}

func TestMergeStreamsRespectsLimit(t *testing.T) {
	now := time.Now().UTC()
	streams := map[string][]model.LogEntry{
		"h1": {
			{ID: 1, Timestamp: now, Message: "a"},
			{ID: 2, Timestamp: now.Add(time.Second), Message: "b"},
			{ID: 3, Timestamp: now.Add(2 * time.Second), Message: "c"},
		},
	}

	out := api.MergeStreams(streams, 2)

	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Entry.Message)
	assert.Equal(t, "b", out[1].Entry.Message)
}

func TestMergeStreamsBreaksTimestampTiesByID(t *testing.T) {
	now := time.Now().UTC()
	streams := map[string][]model.LogEntry{
		"h1": {{ID: 20, Timestamp: now, Message: "later id"}},
		"h2": {{ID: 10, Timestamp: now, Message: "earlier id"}},
	}

	out := api.MergeStreams(streams, 10)

	require.Len(t, out, 2)
	assert.Equal(t, int64(10), out[0].Entry.ID)
	assert.Equal(t, int64(20), out[1].Entry.ID)
}

func TestCursorRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	cursor := api.MergeCursor{
		"h1": {CursorTime: now, CursorID: 5},
		"h2": {Exhausted: true},
	}

	encoded := cursor.Encode()

	require.NotEmpty(t, encoded)
	_, err := base64.URLEncoding.DecodeString(encoded)
	require.NoError(t, err)
	decoded, err := api.DecodeMergeCursor(encoded)
	require.NoError(t, err)
	require.Equal(t, cursor["h1"].CursorID, decoded["h1"].CursorID)
	require.True(t, decoded["h2"].Exhausted)
	_, err = json.Marshal(decoded)
	require.NoError(t, err)
}
