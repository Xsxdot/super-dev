package dbprovision

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
)

func redisTestDataSource(t *testing.T) DataSource {
	t.Helper()
	addr := os.Getenv("SUPERDEV_TEST_REDIS_HOST")
	if addr == "" {
		t.Skip("未设置 SUPERDEV_TEST_REDIS_HOST，跳过 Redis 真实实例测试")
	}
	port := 6379
	if v := os.Getenv("SUPERDEV_TEST_REDIS_PORT"); v != "" {
		if _, err := fmt.Sscanf(v, "%d", &port); err != nil {
			t.Fatalf("SUPERDEV_TEST_REDIS_PORT 不是有效端口: %v", err)
		}
	}
	return DataSource{
		Kind: KindRedis, Name: "it-redis", Host: addr, Port: port,
		Password: os.Getenv("SUPERDEV_TEST_REDIS_PASSWORD"),
	}
}

func TestRedisProbeAgainstRealInstance(t *testing.T) {
	res, err := NewRedisProvisioner().Probe(context.Background(), redisTestDataSource(t))
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if !res.OK {
		t.Fatalf("Probe 未通过: %s", res.Error)
	}
	if res.Facts["databases"] == "" {
		t.Fatal("必须给出 databases 总数")
	}
}

func TestRedisProvisionAndReclaimFlushesOnlyOwnDB(t *testing.T) {
	ds := redisTestDataSource(t)
	ctx := context.Background()
	p := NewRedisProvisioner()

	plan, err := p.Plan(ctx, ds, PlanRequest{ProjectID: "p", Binding: ProjectBinding{Redis: &RedisBinding{}}})
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	res, err := p.Provision(ctx, ds, plan)
	if err != nil {
		t.Fatalf("Provision 失败: %v", err)
	}

	own := mustRedisClient(t, ds, dbIndexOf(t, res))
	neighbor := mustRedisClient(t, ds, 0)
	if err := own.Set(ctx, "k", "v", 0).Err(); err != nil {
		t.Fatalf("写自有 db 失败: %v", err)
	}
	if err := neighbor.Set(ctx, "sentinel", "keep", 0).Err(); err != nil {
		t.Fatalf("写哨兵失败: %v", err)
	}
	defer neighbor.Del(ctx, "sentinel")

	if err := p.Reclaim(ctx, ds, res); err != nil {
		t.Fatalf("Reclaim 失败: %v", err)
	}
	if n, _ := own.DBSize(ctx).Result(); n != 0 {
		t.Fatalf("自有 db 应被清空，剩余 %d", n)
	}
	if value, err := neighbor.Get(ctx, "sentinel").Result(); err != nil || value != "keep" {
		t.Fatal("邻居 db 的数据绝不能被清掉")
	}
	_ = own.Close()
	_ = neighbor.Close()
}

func TestRedisReconcileNeverReportsOrphans(t *testing.T) {
	orphans, err := NewRedisProvisioner().Reconcile(context.Background(), redisTestDataSource(t), nil)
	if err != nil {
		t.Fatalf("Reconcile 失败: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("Redis 无法确证归属，必须永不报告孤儿，实际 %+v", orphans)
	}
}

func mustRedisClient(t *testing.T, ds DataSource, db int) *redis.Client {
	t.Helper()
	client := newRedisClient(ds, db)
	if err := client.Ping(context.Background()).Err(); err != nil {
		client.Close()
		t.Fatalf("连接 Redis db%d 失败: %v", db, err)
	}
	return client
}

func dbIndexOf(t *testing.T, res Resource) int {
	t.Helper()
	index, err := strconv.Atoi(res.Meta["db_index"])
	if err != nil {
		t.Fatalf("资源没有有效 db_index: %+v", res)
	}
	return index
}
