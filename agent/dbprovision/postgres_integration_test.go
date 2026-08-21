package dbprovision

import (
	"context"
	"fmt"
	"os"
	"testing"
)

// pgTestDataSource 从 SUPERDEV_TEST_PG_* 环境变量构造测试用管理连接。
// 未配置时跳过——CI 上没有 PG 是常态，不能因此变红。
func pgTestDataSource(t *testing.T) DataSource {
	t.Helper()
	host := os.Getenv("SUPERDEV_TEST_PG_HOST")
	if host == "" {
		t.Skip("未设置 SUPERDEV_TEST_PG_HOST，跳过 PG 真实实例测试")
	}
	port := 5432
	if v := os.Getenv("SUPERDEV_TEST_PG_PORT"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &port); err != nil {
			t.Fatalf("SUPERDEV_TEST_PG_PORT 不是有效端口: %v", err)
		}
	}
	return DataSource{
		Kind: KindPostgres, Name: "it-pg", Host: host, Port: port,
		User: os.Getenv("SUPERDEV_TEST_PG_USER"), Password: os.Getenv("SUPERDEV_TEST_PG_PASSWORD"),
		Extra: map[string]string{"maintenance_db": "postgres"},
	}
}

func TestPostgresProbeAgainstRealInstance(t *testing.T) {
	ds := pgTestDataSource(t)
	res, err := NewPostgresProvisioner().Probe(context.Background(), ds)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if !res.OK {
		t.Fatalf("Probe 未通过（缺少权限？）: missing=%v error=%s hint=%s", res.Missing, res.Error, res.FixHint)
	}
	for _, cap := range []string{"createdb", "createrole", "pg_signal_backend"} {
		if !res.Capabilities[cap] {
			t.Fatalf("能力 %s 应为 true: %+v", cap, res.Capabilities)
		}
	}
	if res.ServerVer == "" {
		t.Fatal("必须填 ServerVer")
	}
}

func TestPostgresProbeReportsMissingCapability(t *testing.T) {
	ds := pgTestDataSource(t)
	ds.User = os.Getenv("SUPERDEV_TEST_PG_WEAK_USER")
	ds.Password = os.Getenv("SUPERDEV_TEST_PG_WEAK_PASSWORD")
	if ds.User == "" {
		t.Skip("未设置 SUPERDEV_TEST_PG_WEAK_USER，跳过权限不足探测测试")
	}
	res, err := NewPostgresProvisioner().Probe(context.Background(), ds)
	if err != nil {
		t.Fatalf("Probe 返回错误: %v", err)
	}
	if res.OK {
		t.Fatal("弱权限账号不应通过探测")
	}
	if len(res.Missing) == 0 || res.FixHint == "" {
		t.Fatalf("必须给出 Missing 与 FixHint: %+v", res)
	}
}
