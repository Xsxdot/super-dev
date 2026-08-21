// redis.go —— Redis 临时 db 号供给器。
//
// 职责：探测 Redis db 能力、从 1..N-1 选择空闲 db、验证空库并按租约回收。
// 边界：本实现永不主动清理登记表之外的 db；Redis db 号没有可证明的资源前缀，
// 因而 Reconcile 恒返回空，避免误 FLUSH 用户数据。
package dbprovision

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/xsxdot/gokit/logger"
)

// RedisProvisioner 是 Redis 多 db 隔离供给器。
type RedisProvisioner struct{}

// NewRedisProvisioner 创建一个 Redis 供给器。
func NewRedisProvisioner() *RedisProvisioner {
	return &RedisProvisioner{}
}

func init() {
	RegisterProvisioner(NewRedisProvisioner())
}

// Kind 返回 Redis 资源类型标识。
func (p *RedisProvisioner) Kind() string { return KindRedis }

// Probe 探测 Redis 连通性、版本、集群模式、db 总数与当前占用 db。
//
// 返回值 error 只表示探测流程本身无法继续；连接失败、集群模式等结论写在 ProbeResult 中。
func (p *RedisProvisioner) Probe(ctx context.Context, ds DataSource) (ProbeResult, error) {
	log := logger.GetLogger().WithEntryName("DBProvisionRedis").WithFields(map[string]any{
		"host": ds.Host, "port": ds.Port,
	})
	result := ProbeResult{CheckedAt: time.Now(), Facts: map[string]string{}}
	client := newRedisClient(ds, 0)
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("连接 Redis 失败")
		return result, nil
	}
	serverInfo, err := client.Info(ctx, "server").Result()
	if err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("查询 Redis server 信息失败")
		return result, nil
	}
	serverFields := parseRedisInfo(serverInfo)
	result.ServerVer = serverFields["redis_version"]
	clusterInfo, err := client.Info(ctx, "cluster").Result()
	if err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("查询 Redis cluster 信息失败")
		return result, nil
	}
	clusterFields := parseRedisInfo(clusterInfo)
	if clusterFields["cluster_enabled"] == "1" {
		result.Error = "Redis 集群模式不支持多 db，无法按 db 号隔离"
		log.Error("Redis 集群模式拒绝临时 db 供给")
		return result, nil
	}

	total := 16
	config, err := client.ConfigGet(ctx, "databases").Result()
	if err != nil {
		result.Facts["databases_source"] = "fallback"
		log.WithField("fallback", 16).WithErr(err).Warn("Redis CONFIG GET databases 失败，回退默认总数")
	} else if value, ok := config["databases"]; ok {
		parsed, parseErr := strconv.Atoi(value)
		if parseErr != nil || parsed < 1 {
			if parseErr == nil {
				parseErr = fmt.Errorf("databases 必须为正整数: %s", value)
			}
			result.Facts["databases_source"] = "fallback"
			log.WithField("fallback", 16).WithErr(parseErr).Warn("Redis databases 配置无效，回退默认总数")
		} else {
			total = parsed
			result.Facts["databases_source"] = "config"
		}
	} else {
		result.Facts["databases_source"] = "fallback"
		log.WithField("fallback", 16).Warn("Redis CONFIG GET databases 缺少返回值，回退默认总数")
	}
	keyspace, err := client.Info(ctx, "keyspace").Result()
	if err != nil {
		result.Error = err.Error()
		log.WithErr(err).Error("查询 Redis keyspace 信息失败")
		return result, nil
	}
	occupied := occupiedDBIndexes(parseRedisInfo(keyspace))
	result.Facts["databases"] = strconv.Itoa(total)
	result.Facts["occupied_dbs"] = joinIndexes(occupied)
	result.OK = true
	log.WithFields(map[string]any{
		"host": ds.Host, "port": ds.Port, "ok": result.OK,
		"databases": total, "occupied_dbs": result.Facts["occupied_dbs"],
	}).Info("Redis 探测完成")
	return result, nil
}

// Plan 选择最小的空闲 Redis db 号；该阶段只读，不产生任何副作用。
func (p *RedisProvisioner) Plan(ctx context.Context, ds DataSource, req PlanRequest) (Plan, error) {
	if req.Binding.Redis == nil {
		return Plan{}, ErrBindingMissing
	}
	probe, err := p.Probe(ctx, ds)
	if err != nil {
		return Plan{}, err
	}
	if !probe.OK {
		return Plan{}, fmt.Errorf("Redis 探测未通过: %s", probe.Error)
	}
	total, err := strconv.Atoi(probe.Facts["databases"])
	if err != nil {
		return Plan{}, fmt.Errorf("Redis db 总数无效: %w", err)
	}
	occupied, err := parseIndexList(probe.Facts["occupied_dbs"])
	if err != nil {
		return Plan{}, fmt.Errorf("Redis 已占用 db 列表无效: %w", err)
	}
	free := freeDBIndexes(total, occupied, req.TakenHints)
	if len(free) == 0 {
		log := logger.GetLogger().WithEntryName("DBProvisionRedis").WithFields(map[string]any{
			"total": total, "occupied": probe.Facts["occupied_dbs"], "taken": req.TakenHints,
		})
		err := fmt.Errorf("%w: total=%d occupied=%s taken=%v", ErrNoFreeDB, total, probe.Facts["occupied_dbs"], req.TakenHints)
		log.WithErr(err).Warn("Redis 分配池为空")
		return Plan{}, err
	}
	index := free[0]
	name := fmt.Sprintf("db%d", index)
	logger.GetLogger().WithEntryName("DBProvisionRedis").WithFields(map[string]any{
		"db_index": index, "pool_size": len(free),
	}).Info("Redis 选定空闲 db")
	return Plan{
		Kind:         KindRedis,
		ResourceName: name,
		Steps:        []string{fmt.Sprintf("分配空闲 db 号 %d（空库，不克隆）", index)},
		Detail:       map[string]string{"db_index": strconv.Itoa(index)},
	}, nil
}

// Provision 复核选中的 Redis db 为空并返回其连接字符串。
func (p *RedisProvisioner) Provision(ctx context.Context, ds DataSource, plan Plan) (Resource, error) {
	index, err := resourceDBIndex(Resource{Name: plan.ResourceName, Meta: plan.Detail})
	if err != nil {
		return Resource{}, err
	}
	client := newRedisClient(ds, index)
	defer client.Close()
	dbsize, err := client.DBSize(ctx).Result()
	if err != nil {
		logger.GetLogger().WithEntryName("DBProvisionRedis").WithFields(map[string]any{"db_index": index, "step": "dbsize"}).WithErr(err).Error("复核 Redis db 为空失败")
		return Resource{}, fmt.Errorf("复核 Redis db 为空失败: %w", err)
	}
	if dbsize != 0 {
		logger.GetLogger().WithEntryName("DBProvisionRedis").WithFields(map[string]any{"db_index": index, "dbsize": dbsize}).Warn("Redis 选定 db 在复核时已非空")
		return Resource{}, fmt.Errorf("Redis db%d 已有 %d 个 key", index, dbsize)
	}
	logger.GetLogger().WithEntryName("DBProvisionRedis").WithField("db_index", index).Info("Redis 临时 db 供给成功")
	return Resource{
		Kind: KindRedis,
		Name: fmt.Sprintf("db%d", index),
		DSN:  redisDSN(ds, index),
		Meta: map[string]string{"db_index": strconv.Itoa(index)},
	}, nil
}

// Reclaim 只对租约持有的 Redis db 执行 FLUSHDB，ASYNC 不支持时回退同步清理。
func (p *RedisProvisioner) Reclaim(ctx context.Context, ds DataSource, res Resource) error {
	index, err := resourceDBIndex(Resource{Kind: KindRedis, Name: res.Name, Meta: res.Meta})
	if err != nil {
		return err
	}
	client := newRedisClient(ds, index)
	defer client.Close()
	log := logger.GetLogger().WithEntryName("DBProvisionRedis").WithField("db_index", index)
	if err := client.Do(ctx, "SELECT", index).Err(); err != nil {
		log.WithErr(err).Error("选择 Redis 临时 db 失败")
		return fmt.Errorf("选择 Redis db%d 失败: %w", index, err)
	}
	if err := client.Do(ctx, "FLUSHDB", "ASYNC").Err(); err != nil {
		if strings.Contains(strings.ToUpper(err.Error()), "ASYNC") {
			if fallbackErr := client.FlushDB(ctx).Err(); fallbackErr != nil {
				log.WithErr(fallbackErr).Error("Redis 回退同步 FLUSHDB 失败")
				return fmt.Errorf("Redis FLUSHDB 失败: %w", fallbackErr)
			}
		} else {
			log.WithErr(err).Error("Redis FLUSHDB 失败")
			return fmt.Errorf("Redis FLUSHDB 失败: %w", err)
		}
	}
	log.Info("已清理 Redis 临时 db")
	return nil
}

// Reconcile 永远返回空。
//
// Redis db 号没有资源名前缀可依，无法区分用户正在使用的 db 与供给层泄漏的 db；
// 对登记表之外的 db 执行 FLUSHDB 是不可逆的数据破坏，因此本实现永不主动清理。
func (p *RedisProvisioner) Reconcile(context.Context, DataSource, []Resource) ([]Orphan, error) {
	return nil, nil
}

// freeDBIndexes 返回可供给的 Redis db 号，db0 永远保留给实例自身用途。
func freeDBIndexes(total int, occupied []int, taken []string) []int {
	blocked := make(map[int]struct{}, len(occupied)+len(taken))
	for _, index := range occupied {
		blocked[index] = struct{}{}
	}
	for _, name := range taken {
		if !strings.HasPrefix(name, "db") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(name, "db"))
		if err == nil {
			blocked[index] = struct{}{}
		}
	}
	free := make([]int, 0, total)
	for index := 1; index < total; index++ {
		if _, ok := blocked[index]; !ok {
			free = append(free, index)
		}
	}
	return free
}

func newRedisClient(ds DataSource, index int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     net.JoinHostPort(ds.Host, strconv.Itoa(ds.Port)),
		Username: ds.User,
		Password: ds.Password,
		DB:       index,
	})
}

func redisDSN(ds DataSource, index int) string {
	u := &url.URL{
		Scheme: "redis",
		Host:   net.JoinHostPort(ds.Host, strconv.Itoa(ds.Port)),
		Path:   "/" + strconv.Itoa(index),
	}
	if ds.Password != "" || ds.User != "" {
		u.User = url.UserPassword(ds.User, ds.Password)
	}
	return u.String()
}

func resourceDBIndex(res Resource) (int, error) {
	value := res.Meta["db_index"]
	if value == "" {
		if !strings.HasPrefix(res.Name, "db") {
			return 0, fmt.Errorf("Redis 资源名无效: %s", res.Name)
		}
		value = strings.TrimPrefix(res.Name, "db")
	}
	index, err := strconv.Atoi(value)
	if err != nil || index <= 0 {
		return 0, fmt.Errorf("Redis db 号无效: %s", value)
	}
	return index, nil
}

func parseRedisInfo(info string) map[string]string {
	fields := make(map[string]string)
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[key] = value
		}
	}
	return fields
}

func occupiedDBIndexes(fields map[string]string) []int {
	var indexes []int
	for key := range fields {
		if !strings.HasPrefix(key, "db") {
			continue
		}
		index, err := strconv.Atoi(strings.TrimPrefix(key, "db"))
		if err == nil {
			indexes = append(indexes, index)
		}
	}
	sort.Ints(indexes)
	return indexes
}

func joinIndexes(indexes []int) string {
	parts := make([]string, 0, len(indexes))
	for _, index := range indexes {
		parts = append(parts, strconv.Itoa(index))
	}
	return strings.Join(parts, ",")
}

func parseIndexList(value string) ([]int, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	indexes := make([]int, 0, len(parts))
	for _, part := range parts {
		index, err := strconv.Atoi(part)
		if err != nil {
			return nil, err
		}
		indexes = append(indexes, index)
	}
	return indexes, nil
}
