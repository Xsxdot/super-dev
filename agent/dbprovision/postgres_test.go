package dbprovision

import (
	"strings"
	"testing"
)

func TestAdminDSNUsesMaintenanceDatabaseAndEscapesPassword(t *testing.T) {
	ds := DataSource{
		Kind: KindPostgres, Host: "127.0.0.1", Port: 5432,
		User: "sdev_admin", Password: "p@ss:w/rd",
		Extra: map[string]string{"maintenance_db": "postgres"},
	}
	got := adminDSN(ds, "")
	if !strings.Contains(got, "/postgres") {
		t.Fatalf("未使用维护库: %s", got)
	}
	if strings.Contains(got, "p@ss:w/rd") {
		t.Fatalf("密码必须 URL 编码后出现: %s", got)
	}
	if !strings.Contains(got, "sslmode=") {
		t.Fatalf("必须显式带 sslmode: %s", got)
	}

	target := adminDSN(ds, "sdev_eph_tk_aabbcc")
	if !strings.Contains(target, "/sdev_eph_tk_aabbcc") {
		t.Fatalf("指定库名时应连到该库: %s", target)
	}
}

func TestAdminDSNDefaultsMaintenanceDBToPostgres(t *testing.T) {
	ds := DataSource{Kind: KindPostgres, Host: "h", Port: 5432, User: "u", Password: "p"}
	if !strings.Contains(adminDSN(ds, ""), "/postgres") {
		t.Fatal("Extra 缺 maintenance_db 时应回退 postgres")
	}
}
