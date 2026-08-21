// provision.go —— 租约与临时资源的 SQLite 持久化仓储。
//
// 职责：保存租约、资源状态、资源元数据及并发占用查询，支撑重启恢复与回收。
// 边界：只做存取和数据库约束，不做生命周期决策，不认识 PG/Redis 的资源语义。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/dbprovision"
)

// ErrResourceSlotTaken 表示同一数据源上的资源槽位已被其他未回收资源占用。
var ErrResourceSlotTaken = errors.New("provision resource slot taken")

// ResourceRow 是数据库中一行资源登记，不包含明文 DSN。
type ResourceRow struct {
	ID           string
	LeaseID      string
	DataSourceID string
	Kind         string
	Name         string
	Meta         map[string]string
	Status       string
}

// InsertLease 插入一条 active 状态的租约。
//
// 注意：时间按 Unix 秒保存，与现有 store 表保持一致。
func (s *Store) InsertLease(lease dbprovision.Lease) error {
	_, err := s.db.Exec(`
		INSERT INTO provision_leases (id, project_id, purpose, created_at, expires_at, renew_count, status)
		VALUES (?, ?, ?, ?, ?, ?, 'active')
	`, lease.ID, lease.ProjectID, lease.Purpose, lease.CreatedAt.Unix(), lease.ExpiresAt.Unix(), lease.RenewCount)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "insert_lease").WithErr(err).Error("插入租约失败")
	}
	return err
}

// InsertResource 先登记一条 creating 状态的资源并返回资源 ID。
//
// 注意：部分唯一索引是 Redis db 号并发分配的数据库级防撞锁；撞锁是预期的重选路径，
// 因此按 Info 记录而不是 Error，调用方应重新规划资源。
func (s *Store) InsertResource(leaseID, datasourceID string, res dbprovision.Resource) (string, error) {
	meta := []byte("{}")
	if res.Meta != nil {
		var err error
		meta, err = json.Marshal(res.Meta)
		if err != nil {
			logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "insert_resource").WithErr(err).Error("序列化资源元数据失败")
			return "", err
		}
	}
	id := uuid.NewString()
	_, err := s.db.Exec(`
		INSERT INTO provision_resources (id, lease_id, datasource_id, kind, name, meta_json, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'creating', ?)
	`, id, leaseID, datasourceID, res.Kind, res.Name, string(meta), time.Now().Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			logger.GetLogger().WithEntryName("ProvisionStore").WithFields(map[string]any{
				"datasource_id": datasourceID, "kind": res.Kind, "name": res.Name,
			}).Info("资源槽位已被占用，调用方应重选")
			return "", ErrResourceSlotTaken
		}
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "insert_resource").WithErr(err).Error("插入资源登记失败")
		return "", err
	}
	return id, nil
}

// MarkResourceActive 把资源登记标记为 active。
func (s *Store) MarkResourceActive(resourceID string) error {
	_, err := s.db.Exec(`UPDATE provision_resources SET status = 'active' WHERE id = ?`, resourceID)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "mark_resource_active").WithErr(err).Error("标记资源 active 失败")
	}
	return err
}

// MarkResourceReclaimed 把资源登记标记为 reclaimed，释放其唯一槽位。
func (s *Store) MarkResourceReclaimed(resourceID string) error {
	_, err := s.db.Exec(`UPDATE provision_resources SET status = 'reclaimed' WHERE id = ?`, resourceID)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("resource_id", resourceID).WithErr(err).Error("标记资源 reclaimed 失败")
		return err
	}
	logger.GetLogger().WithEntryName("ProvisionStore").WithField("resource_id", resourceID).Debug("资源已标记 reclaimed")
	return nil
}

// UpdateLeaseExpiry 更新租约到期时间与续租次数。
func (s *Store) UpdateLeaseExpiry(leaseID string, expiresAt time.Time, renewCount int) error {
	_, err := s.db.Exec(`UPDATE provision_leases SET expires_at = ?, renew_count = ? WHERE id = ?`, expiresAt.Unix(), renewCount, leaseID)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "update_lease_expiry").WithErr(err).Error("更新租约到期时间失败")
	}
	return err
}

// MarkLeaseReleased 把租约标记为 released。
func (s *Store) MarkLeaseReleased(leaseID string) error {
	_, err := s.db.Exec(`UPDATE provision_leases SET status = 'released' WHERE id = ?`, leaseID)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("lease_id", leaseID).WithErr(err).Error("标记租约 released 失败")
		return err
	}
	logger.GetLogger().WithEntryName("ProvisionStore").WithField("lease_id", leaseID).Debug("租约已标记 released")
	return nil
}

// GetLease 返回租约及其全部资源登记。
//
// 注意：返回的资源行不含 DSN；DSN 只存在进程内供给结果，不落 SQLite。
func (s *Store) GetLease(leaseID string) (dbprovision.Lease, []ResourceRow, error) {
	var lease dbprovision.Lease
	var createdAt, expiresAt int64
	var status string
	err := s.db.QueryRow(`
		SELECT id, project_id, purpose, created_at, expires_at, renew_count, status
		FROM provision_leases WHERE id = ?
	`, leaseID).Scan(&lease.ID, &lease.ProjectID, &lease.Purpose, &createdAt, &expiresAt, &lease.RenewCount, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return dbprovision.Lease{}, nil, dbprovision.ErrLeaseNotFound
	}
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "get_lease").WithErr(err).Error("读取租约失败")
		return dbprovision.Lease{}, nil, err
	}
	lease.CreatedAt = time.Unix(createdAt, 0)
	lease.ExpiresAt = time.Unix(expiresAt, 0)
	rows, err := s.listResourceRows(`SELECT id, lease_id, datasource_id, kind, name, meta_json, status FROM provision_resources WHERE lease_id = ? ORDER BY created_at, id`, leaseID)
	if err != nil {
		return dbprovision.Lease{}, nil, err
	}
	_ = status
	return lease, rows, nil
}

// ListLeases 返回指定项目的 active 租约；projectID 为空时返回全部 active 租约。
func (s *Store) ListLeases(projectID string) ([]dbprovision.Lease, error) {
	query := `SELECT id, project_id, purpose, created_at, expires_at, renew_count FROM provision_leases WHERE status = 'active'`
	args := []any{}
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, projectID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	return s.listLeases(query, args...)
}

// ListExpiredLeases 返回到期且仍处于 active 状态的租约。
func (s *Store) ListExpiredLeases(now time.Time) ([]dbprovision.Lease, error) {
	return s.listLeases(`
		SELECT id, project_id, purpose, created_at, expires_at, renew_count
		FROM provision_leases WHERE status = 'active' AND expires_at <= ?
		ORDER BY expires_at, id
	`, now.Unix())
}

// CountActiveLeases 返回项目 active 租约数。
//
// 注意：这里有意把已过期但尚未巡检回收的租约计入配额，宁可暂时拒绝也不能超发资源。
func (s *Store) CountActiveLeases(projectID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM provision_leases WHERE project_id = ? AND status = 'active'`, projectID).Scan(&count)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "count_active_leases").WithErr(err).Error("统计项目活跃租约失败")
	}
	return count, err
}

// CountActiveLeasesByDataSource 返回仍占用指定数据源资源的 active 租约数。
func (s *Store) CountActiveLeasesByDataSource(datasourceID string) (int, error) {
	var count int
	err := s.db.QueryRow(`
		SELECT count(DISTINCT l.id)
		FROM provision_leases l
		JOIN provision_resources r ON r.lease_id = l.id
		WHERE l.status = 'active' AND r.datasource_id = ? AND r.status <> 'reclaimed'
	`, datasourceID).Scan(&count)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "count_active_leases_by_datasource").WithErr(err).Error("按数据源统计活跃租约失败")
	}
	return count, err
}

// ListActiveResourceNames 返回数据源上指定 kind 的 active 资源名，供规划阶段避让已知占用。
func (s *Store) ListActiveResourceNames(datasourceID, kind string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT name FROM provision_resources
		WHERE datasource_id = ? AND kind = ? AND status <> 'reclaimed'
		ORDER BY name
	`, datasourceID, kind)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_active_resource_names").WithErr(err).Error("查询活跃资源名称失败")
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_active_resource_names").WithErr(err).Error("读取活跃资源名称失败")
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// ListAllActiveResources 返回所有未回收资源，供启动对账过滤已知登记。
func (s *Store) ListAllActiveResources() ([]ResourceRow, error) {
	return s.listResourceRows(`SELECT id, lease_id, datasource_id, kind, name, meta_json, status FROM provision_resources WHERE status <> 'reclaimed' ORDER BY created_at, id`)
}

func (s *Store) listLeases(query string, args ...any) ([]dbprovision.Lease, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_leases").WithErr(err).Error("查询租约失败")
		return nil, err
	}
	defer rows.Close()
	var leases []dbprovision.Lease
	for rows.Next() {
		var lease dbprovision.Lease
		var createdAt, expiresAt int64
		if err := rows.Scan(&lease.ID, &lease.ProjectID, &lease.Purpose, &createdAt, &expiresAt, &lease.RenewCount); err != nil {
			logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_leases").WithErr(err).Error("读取租约列表失败")
			return nil, err
		}
		lease.CreatedAt = time.Unix(createdAt, 0)
		lease.ExpiresAt = time.Unix(expiresAt, 0)
		leases = append(leases, lease)
	}
	return leases, rows.Err()
}

func (s *Store) listResourceRows(query string, args ...any) ([]ResourceRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_resources").WithErr(err).Error("查询资源登记失败")
		return nil, err
	}
	defer rows.Close()
	var result []ResourceRow
	for rows.Next() {
		var row ResourceRow
		var metaJSON string
		if err := rows.Scan(&row.ID, &row.LeaseID, &row.DataSourceID, &row.Kind, &row.Name, &metaJSON, &row.Status); err != nil {
			logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_resources").WithErr(err).Error("读取资源列表失败")
			return nil, err
		}
		if err := json.Unmarshal([]byte(metaJSON), &row.Meta); err != nil {
			logger.GetLogger().WithEntryName("ProvisionStore").WithField("op", "list_resources").WithErr(err).Error("解析资源元数据失败")
			return nil, fmt.Errorf("解析资源元数据失败: %w", err)
		}
		result = append(result, row)
	}
	return result, rows.Err()
}
