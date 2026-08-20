package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestTokenOriginExpires 钉死登记条目会过期，不会无限增长。
func TestTokenOriginExpires(t *testing.T) {
	now := time.Now()
	o := &approvalTokenOrigin{
		entries: map[string]approvalTokenOriginEntry{},
		now:     func() time.Time { return now },
	}
	o.Remember("tok", "h1", now.Add(time.Minute))
	require.Equal(t, "h1", o.OriginOf("tok"))

	now = now.Add(2 * time.Minute)
	require.Equal(t, "", o.OriginOf("tok"), "过期后不得再认这张 token 的来源")
	require.Empty(t, o.entries, "过期条目应被惰性清理，否则表会随时间无限增长")
}
