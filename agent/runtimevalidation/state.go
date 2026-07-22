// state.go 持久化固定 active marker、runner-only foundation lock 与当前-run cleanup journal。
//
// 职责：
//   - 让异常强杀后的旧 campaign 在下一次运行 fail closed 为 BLOCKED
//   - 对每次 mutation 按 intent/acquired/released 顺序 append 并 fsync
//   - 提供只排斥其他 validation runner 的 foundation 互斥锁
//
// 边界：
//   - 不自动 replay journal、不启动 recovery Agent，也不锁普通 Agent
//   - marker/journal 不记录 credential、token、cookie 或通用工作流状态
package runtimevalidation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
)

const activeMarkerFilename = "active.json"

// FoundationStateRoot 返回 foundation 同级固定的 profile state 目录。
//
// 参数：
//   - foundationPath: 专用 foundation 根目录
//   - profileID: marker 绑定的稳定 profile ID
//
// 返回：
//   - <foundation-parent>/.runtime-validation/<profile-id>
func FoundationStateRoot(foundationPath, profileID string) string {
	return filepath.Join(filepath.Dir(foundationPath), ".runtime-validation", profileID)
}

// WriteActiveMarker 以 fsync+atomic rename 写入本次 campaign marker。
//
// 参数：
//   - stateRoot: FoundationStateRoot 返回的固定目录
//   - marker: 不含 secret 的 campaign、bundle、clone 和 remote root 身份
//
// 返回：
//   - 旧 marker 已存在、字段缺失或持久化失败时的错误
//
// 注意：创建后只有完整 cleanup 才能调用 RemoveActiveMarker。
func WriteActiveMarker(stateRoot string, marker ActiveMarker) error {
	if strings.TrimSpace(marker.CampaignID) == "" || strings.TrimSpace(marker.ClonePath) == "" {
		return fmt.Errorf("active marker requires campaign_id and clone_path")
	}
	path := filepath.Join(stateRoot, activeMarkerFilename)
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		return fmt.Errorf("active marker already exists at %s", path)
	}
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return err
	}
	if err := atomicWriteJSON(path, marker, 0o600); err != nil {
		return err
	}
	logger.GetLogger().WithEntryName("RuntimeValidationState").WithFields(map[string]any{"campaign_id": marker.CampaignID, "state_root": stateRoot}).Info("runtime validation active marker 已创建")
	return nil
}

// ReadActiveMarker 读取并校验固定 active marker。
func ReadActiveMarker(stateRoot string) (ActiveMarker, error) {
	var marker ActiveMarker
	if err := readJSONFile(filepath.Join(stateRoot, activeMarkerFilename), &marker); err != nil {
		return marker, err
	}
	if strings.TrimSpace(marker.CampaignID) == "" || strings.TrimSpace(marker.ClonePath) == "" {
		return marker, fmt.Errorf("active marker is incomplete")
	}
	return marker, nil
}

// CheckActiveMarker 派生旧 marker 的 strict admission gate。
//
// 返回：
//   - marker 不存在时 PASS
//   - marker 存在时包含旧 campaign/reset 上下文的 BLOCKED
//   - marker 损坏或读取失败时的错误
func CheckActiveMarker(stateRoot string) (CheckResult, error) {
	marker, err := ReadActiveMarker(stateRoot)
	if os.IsNotExist(err) {
		return CheckResult{ID: "active-marker", Status: StatusPass}, nil
	}
	if err != nil {
		return CheckResult{}, err
	}
	message := fmt.Sprintf("previous campaign %s remains active; clone=%s remote_root=%s", marker.CampaignID, marker.ClonePath, marker.RemoteRoot)
	logger.GetLogger().WithEntryName("RuntimeValidationState").WithFields(map[string]any{"campaign_id": marker.CampaignID, "clone_path": marker.ClonePath, "remote_root": marker.RemoteRoot}).Error("发现旧 runtime validation active marker，阻止新 campaign")
	return CheckResult{ID: "active-marker", Status: StatusBlocked, Cause: Cause{Code: "active_marker_present", Message: message, Source: "active-marker"}}, nil
}

// RemoveActiveMarker 在全部 cleanup/attestation gate 通过后删除 marker 并 fsync 父目录。
func RemoveActiveMarker(stateRoot string) error {
	path := filepath.Join(stateRoot, activeMarkerFilename)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := syncDirectory(stateRoot); err != nil {
		return err
	}
	logger.GetLogger().WithEntryName("RuntimeValidationState").WithField("state_root", stateRoot).Info("runtime validation active marker 已删除")
	return nil
}

// FoundationLock 是只排斥同一 profile 其他 validation runner 的文件锁。
type FoundationLock struct {
	file *os.File
	once sync.Once
}

// AcquireFoundationLock 非阻塞获取 runner-only foundation lock。
//
// 参数：
//   - stateRoot: profile 固定 state 目录
//
// 返回：
//   - 成功锁句柄
//   - 已有 runner 或文件锁失败错误
//
// 注意：该锁不约束普通 Agent；机器准备合同必须保证专用 foundation 不同时被普通 Agent 使用。
func AcquireFoundationLock(stateRoot string) (*FoundationLock, error) {
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(stateRoot, "runner.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := tryLockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("validation foundation is already locked: %w", err)
	}
	logger.GetLogger().WithEntryName("RuntimeValidationState").WithField("state_root", stateRoot).Info("已获取 validation foundation runner-only 锁")
	return &FoundationLock{file: file}, nil
}

// Release 释放 runner-only foundation lock。
func (l *FoundationLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	var result error
	l.once.Do(func() {
		if err := unlockFile(l.file); err != nil {
			result = err
		}
		if err := l.file.Close(); err != nil && result == nil {
			result = err
		}
	})
	return result
}

// JournalEntry 是 append-only cleanup journal 的一条 fsync 后事实。
type JournalEntry struct {
	Sequence  int64          `json:"sequence"`
	Timestamp string         `json:"timestamp"`
	Campaign  string         `json:"campaign_id"`
	Phase     string         `json:"phase"`
	Kind      string         `json:"resource_kind"`
	ID        string         `json:"resource_id"`
	Owner     string         `json:"owner"`
	Preimage  map[string]any `json:"preimage,omitempty"`
}

// JournalSnapshot 是当前进程 journal 的完整性和未释放资源投影。
type JournalSnapshot struct {
	Complete   bool       `json:"complete"`
	EntryCount int        `json:"entry_count"`
	Unreleased []Residual `json:"unreleased"`
}

// CleanupJournal 管理当前 run 的 append-only intent/acquired/released 记录。
type CleanupJournal struct {
	mu       sync.Mutex
	file     *os.File
	writer   *bufio.Writer
	campaign string
	now      func() time.Time
	sequence int64
	states   map[string]string
	entries  int
}

// OpenCleanupJournal 创建一个新的 0600 journal。
//
// 参数：
//   - path: campaign state 目录内的新 JSONL 路径
//   - campaignID: marker 绑定的 campaign
//   - now: 可注入测试时钟；nil 使用 time.Now
//
// 返回：
//   - 空 journal writer
//   - 路径已存在或打开失败错误
func OpenCleanupJournal(path, campaignID string, now func() time.Time) (*CleanupJournal, error) {
	if strings.TrimSpace(campaignID) == "" {
		return nil, fmt.Errorf("cleanup journal campaign_id is required")
	}
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &CleanupJournal{file: file, writer: bufio.NewWriter(file), campaign: campaignID, now: now, states: map[string]string{}}, nil
}

// Intent 在 mutation 前写入并 fsync 必要的非秘密 pre-image。
func (j *CleanupJournal) Intent(kind, id, owner string, preimage map[string]any) error {
	return j.append("intent", kind, id, owner, preimage)
}

// Acquired 在 mutation 成功后写入并 fsync acquired 事实。
func (j *CleanupJournal) Acquired(kind, id, owner string) error {
	return j.append("acquired", kind, id, owner, nil)
}

// Released 在正常 cleanup 成功后写入并 fsync released 事实。
func (j *CleanupJournal) Released(kind, id, owner string) error {
	return j.append("released", kind, id, owner, nil)
}

// Snapshot 返回 journal 当前完整性与未释放资源；不读取磁盘重放。
func (j *CleanupJournal) Snapshot() JournalSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	residuals := make([]Residual, 0)
	for key, phase := range j.states {
		if phase == "released" {
			continue
		}
		parts := strings.SplitN(key, "\x00", 2)
		residuals = append(residuals, Residual{Kind: parts[0], ID: parts[1], Detail: "journal phase=" + phase})
	}
	sort.Slice(residuals, func(i, k int) bool {
		if residuals[i].Kind == residuals[k].Kind {
			return residuals[i].ID < residuals[k].ID
		}
		return residuals[i].Kind < residuals[k].Kind
	})
	return JournalSnapshot{Complete: len(residuals) == 0, EntryCount: j.entries, Unreleased: residuals}
}

// Close 刷新并关闭 journal 文件。
func (j *CleanupJournal) Close() error {
	if j == nil || j.file == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	flushErr := j.writer.Flush()
	syncErr := j.file.Sync()
	closeErr := j.file.Close()
	for _, err := range []error{flushErr, syncErr, closeErr} {
		if err != nil {
			return err
		}
	}
	return nil
}

func (j *CleanupJournal) append(phase, kind, id, owner string, preimage map[string]any) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if strings.TrimSpace(kind) == "" || strings.TrimSpace(id) == "" || owner != j.campaign {
		return fmt.Errorf("journal resource identity/owner is invalid")
	}
	if containsSensitiveValue(preimage) {
		return fmt.Errorf("journal preimage contains sensitive keys")
	}
	key := kind + "\x00" + id
	previous := j.states[key]
	switch phase {
	case "intent":
		if previous != "" {
			return fmt.Errorf("journal resource %s/%s already has phase %s", kind, id, previous)
		}
	case "acquired":
		if previous != "intent" {
			return fmt.Errorf("journal resource %s/%s acquired without intent", kind, id)
		}
	case "released":
		if previous != "acquired" {
			return fmt.Errorf("journal resource %s/%s released without acquired", kind, id)
		}
	default:
		return fmt.Errorf("unknown journal phase %s", phase)
	}
	j.sequence++
	entry := JournalEntry{Sequence: j.sequence, Timestamp: j.now().UTC().Format(time.RFC3339Nano), Campaign: j.campaign, Phase: phase, Kind: kind, ID: id, Owner: owner, Preimage: preimage}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if _, err := j.writer.Write(append(raw, '\n')); err != nil {
		return err
	}
	if err := j.writer.Flush(); err != nil {
		return err
	}
	if err := j.file.Sync(); err != nil {
		return err
	}
	j.states[key] = phase
	j.entries++
	logger.GetLogger().WithEntryName("RuntimeValidationJournal").WithFields(map[string]any{"campaign_id": j.campaign, "phase": phase, "resource_kind": kind, "resource_id": id, "sequence": j.sequence}).Info("runtime validation cleanup journal 已持久化")
	return nil
}

func containsSensitiveValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ToLower(key)
			for _, forbidden := range []string{"token", "secret", "password", "cookie", "authorization", "private_key"} {
				if strings.Contains(normalized, forbidden) {
					return true
				}
			}
			if containsSensitiveValue(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsSensitiveValue(nested) {
				return true
			}
		}
	}
	return false
}

func atomicWriteJSON(path string, value any, mode os.FileMode) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	for _, candidate := range []error{writeErr, syncErr, closeErr} {
		if candidate != nil {
			_ = os.Remove(temporary)
			return candidate
		}
	}
	if err := atomicReplace(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
