package logparse_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/superdev/agent/logparse"
)

func TestDetectLevel_structuredField(t *testing.T) {
	line := `16:37:25 [server] INFO time="2026-05-21 16:37:25" level=error msg="手动刷新绑定失败"`
	assert.Equal(t, "ERROR", logparse.DetectLevel(line))

	line = `time="2026-05-21 16:37:25" level=warning msg="API handler 返回错误"`
	assert.Equal(t, "WARN", logparse.DetectLevel(line))
}

func TestDetectLevel_bracketMarkers(t *testing.T) {
	assert.Equal(t, "ERROR", logparse.DetectLevel("2024-01-01 10:00:00 [ERROR] database connection failed"))
	assert.Equal(t, "WARN", logparse.DetectLevel("[WARN] slow query detected"))
}

func TestDetectLevel_keywordFallback(t *testing.T) {
	assert.Equal(t, "ERROR", logparse.DetectLevel("FATAL: out of memory"))
	assert.Equal(t, "WARN", logparse.DetectLevel("something WARNING happened"))
	assert.Equal(t, "DEBUG", logparse.DetectLevel("TRACE: entering handler"))
	assert.Equal(t, "INFO", logparse.DetectLevel("server started on port 8080"))
}

func TestDetectLevel_plainInfoPrefix(t *testing.T) {
	assert.Equal(t, "INFO", logparse.DetectLevel("16:37:25 [server] INFO SELECT * FROM users"))
}
