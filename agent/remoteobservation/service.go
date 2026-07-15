// Package remoteobservation 提供认证后的安全远程观察能力。
//
// 职责：
//   - 把本机系统身份收敛为不可逆的安全事实
//   - 从 collector 实际运行态计算 active collector 数
//   - 只从内部 Host IP 字段生成固定端口直连探测候选
//
// 边界：
//   - 不返回或记录 machine-id 原文、hostname、IP 或 dial 错误原文
//   - 不接受调用方提供的 address 或 port
//   - 不派生 PASS/FAIL/BLOCKED，只返回可供上层判定的观察事实
package remoteobservation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/xsxdot/gokit/logger"
	"github.com/xsxdot/super-dev/agent/model"
)

const (
	// DirectExposurePort 是 Agent 直连暴露观察的唯一目标端口。
	DirectExposurePort = 57017
	defaultDialTimeout = 2 * time.Second
)

// ErrHostNotFound 表示指定 host_id 不在内部 Host 存储中。
var ErrHostNotFound = errors.New("remote observation host not found")

// SystemFacts 是 NodeStatus 对外暴露的安全系统身份。
//
// 注意：
//   - MachineIDSHA256 是 trim 后 machine-id/MachineGuid 的 SHA-256，原文永不离开本模块
//   - 该结构故意不包含 hostname 或任何网络地址
type SystemFacts struct {
	OS              string `json:"os"`
	KernelArch      string `json:"kernel_arch"`
	AgentArch       string `json:"agent_arch"`
	AgentNodeID     string `json:"agent_node_id"`
	MachineIDSHA256 string `json:"machine_id_sha256"`
}

// DirectExposureObservation 是固定端口直连探测的安全计数结果。
//
// 注意：
//   - 全部 dial 失败仍是观察成功，调用方由计数派生结论
//   - 结构不包含候选 IP、dial 错误或内部 Host 名称
type DirectExposureObservation struct {
	HostID            string    `json:"host_id"`
	FixedPort         int       `json:"fixed_port"`
	CandidateCount    int       `json:"candidate_count"`
	DialAttemptCount  int       `json:"dial_attempt_count"`
	ReachableCount    int       `json:"reachable_count"`
	InconclusiveCount int       `json:"inconclusive_count"`
	CheckedAtUTC      time.Time `json:"checked_at_utc"`
}

// Observer 是 API 层使用的远程观察模块边界。
//
// 实现负责获取与脱敏；handler 只传 host_id 并投影返回值。
type Observer interface {
	// LocalSystemFacts 返回已脱敏且可缓存的本机系统事实。
	LocalSystemFacts(ctx context.Context) SystemFacts
	// ObserveDirectExposure 按 host_id 执行固定 57017/TCP 只读探测。
	ObserveDirectExposure(ctx context.Context, hostID string) (DirectExposureObservation, error)
}

// HostAddressFacts 是从内部 Host 存储传入探测模块的最小字段集。
//
// 该类型的所有字段均使用 json:"-"，不能作为 HTTP 响应投影。
type HostAddressFacts struct {
	HostID    string `json:"-"`
	PublicIP  string `json:"-"`
	PrivateIP string `json:"-"`
}

// HostSource 按 host_id 返回内部 Host 的最小探测视图。
type HostSource interface {
	// ObservationHost 只返回探测所需的 Host ID 和 literal-IP 候选字段。
	ObservationHost(ctx context.Context, hostID string) (HostAddressFacts, bool, error)
}

// Service 统一实现系统身份与固定端口直连观察。
type Service struct {
	agentNodeID     string
	operatingSystem string
	agentArch       string
	kernelArch      func() string
	machineID       func() ([]byte, error)
	hostSource      HostSource
	dialContext     func(ctx context.Context, network, address string) (net.Conn, error)
	now             func() time.Time
	dialTimeout     time.Duration
	systemOnce      sync.Once
	systemFacts     SystemFacts
}

// NewService 创建一个生产远程观察服务。
//
// 参数：
//   - agentNodeID: identity 模块生成的稳定 Agent node ID
//   - hostSource: 只返回 Host ID/PublicIP/PrivateIP 的内部数据源
//
// 返回：
//   - 只读、固定端口的远程观察服务
func NewService(agentNodeID string, hostSource HostSource) *Service {
	dialer := &net.Dialer{Timeout: defaultDialTimeout}
	return &Service{
		agentNodeID:     strings.TrimSpace(agentNodeID),
		operatingSystem: runtime.GOOS,
		agentArch:       runtime.GOARCH,
		kernelArch:      readKernelArchitecture,
		machineID:       readMachineIdentity,
		hostSource:      hostSource,
		dialContext:     dialer.DialContext,
		now:             time.Now,
		dialTimeout:     defaultDialTimeout,
	}
}

// LocalSystemFacts 返回不包原始机器身份的本机系统事实。
func (s *Service) LocalSystemFacts(_ context.Context) SystemFacts {
	s.systemOnce.Do(func() {
		s.systemFacts = s.collectLocalSystemFacts()
	})
	return s.systemFacts
}

func (s *Service) collectLocalSystemFacts() SystemFacts {
	log := logger.GetLogger().WithEntryName("RemoteObservation")
	log.Info("开始收集安全系统身份")
	facts := SystemFacts{
		OS:          strings.TrimSpace(s.operatingSystem),
		AgentArch:   strings.TrimSpace(s.agentArch),
		AgentNodeID: strings.TrimSpace(s.agentNodeID),
	}
	if s.kernelArch != nil {
		facts.KernelArch = strings.TrimSpace(s.kernelArch())
	}
	if s.machineID != nil {
		raw, err := s.machineID()
		normalized := strings.TrimSpace(string(raw))
		if err == nil && normalized != "" {
			digest := sha256.Sum256([]byte(normalized))
			facts.MachineIDSHA256 = fmt.Sprintf("%x", digest[:])
		}
	}
	log.WithFields(map[string]any{
		"os": facts.OS, "kernel_arch": facts.KernelArch, "agent_arch": facts.AgentArch,
		"machine_identity_available": facts.MachineIDSHA256 != "",
	}).Info("安全系统身份收集完成")
	return facts
}

// ObserveDirectExposure 仅对内部 Host 提供的 literal IP 执行 57017/TCP 探测。
//
// 参数：
//   - ctx: 控制整个观察生命周期
//   - hostID: 待查询的内部 Host ID，是唯一调用方输入
//
// 返回：
//   - 不含 IP 或错误原文的安全计数结果
//   - Host 不存在或内部数据源失败时的模块错误
func (s *Service) ObserveDirectExposure(ctx context.Context, hostID string) (DirectExposureObservation, error) {
	hostID = strings.TrimSpace(hostID)
	log := logger.GetLogger().WithEntryName("RemoteObservation").WithField("host_id", hostID)
	log.Info("开始固定端口直连暴露观察")
	if s.hostSource == nil {
		log.WithField("cause_code", "host_source_unavailable").Error("固定端口直连暴露观察失败")
		return DirectExposureObservation{}, fmt.Errorf("remote observation host source unavailable")
	}
	host, found, err := s.hostSource.ObservationHost(ctx, hostID)
	if err != nil {
		log.WithField("cause_code", "host_lookup_failed").Error("固定端口直连暴露观察失败")
		return DirectExposureObservation{}, fmt.Errorf("lookup remote observation host: %w", err)
	}
	if !found {
		log.WithField("cause_code", "host_not_found").Error("固定端口直连暴露观察失败")
		return DirectExposureObservation{}, ErrHostNotFound
	}

	candidates := literalIPCandidates(host.PublicIP, host.PrivateIP)
	result := DirectExposureObservation{
		HostID: hostID, FixedPort: DirectExposurePort, CandidateCount: len(candidates),
	}
	if len(candidates) > 0 && s.dialContext == nil {
		// 没有 dialer 时并未发生真实尝试；不能把能力缺失伪装成“不可达”。
		result.InconclusiveCount = len(candidates)
		result.CheckedAtUTC = s.currentTime().UTC()
		log.WithFields(map[string]any{
			"cause_code": "dialer_unavailable", "candidate_count": result.CandidateCount,
			"dial_attempt_count": result.DialAttemptCount, "inconclusive_count": result.InconclusiveCount,
		}).Info("固定端口直连暴露观察无法形成可达性事实")
		return result, nil
	}
	for index, candidate := range candidates {
		if ctx.Err() != nil {
			// 父 context 在本候选尚未尝试时已终止，保留“未尝试”计数事实。
			result.InconclusiveCount += len(candidates) - index
			log.WithFields(map[string]any{
				"cause_code": directDialCauseCode(ctx.Err()), "remaining_candidate_count": len(candidates) - index,
			}).Info("固定端口直连暴露观察被上层终止")
			break
		}
		attemptLog := log.WithFields(map[string]any{
			"candidate_index": index, "fixed_port": DirectExposurePort,
		})
		attemptLog.Info("开始直连候选探测")
		result.DialAttemptCount++
		dialCtx, cancel := context.WithTimeout(ctx, s.effectiveDialTimeout())
		conn, dialErr := s.dial(dialCtx, net.JoinHostPort(candidate, fmt.Sprintf("%d", DirectExposurePort)))
		cancel()
		switch {
		case dialErr == nil && conn != nil:
			result.ReachableCount++
			_ = conn.Close()
			attemptLog.WithField("outcome", "reachable").Info("直连候选探测完成")
		case dialErr == nil:
			result.InconclusiveCount++
			attemptLog.WithFields(map[string]any{
				"outcome": "inconclusive", "cause_code": "nil_connection",
			}).Info("直连候选探测完成")
		case ctx.Err() != nil:
			// 父 context 终止意味着完整 dial 观察未形成，不能判为“不可达”。
			result.InconclusiveCount++
			attemptLog.WithFields(map[string]any{
				"outcome": "inconclusive", "cause_code": directDialCauseCode(ctx.Err()),
			}).Info("直连候选探测完成")
			if remaining := len(candidates) - index - 1; remaining > 0 {
				result.InconclusiveCount += remaining
			}
		default:
			// 只要 dialer 真实执行并返回错误，refused、timeout、no-route
			// 都是“当次不可达”事实；错误类型和原文不向外投影。
			attemptLog.WithField("outcome", "unreachable").Info("直连候选探测完成")
		}
		if ctx.Err() != nil {
			break
		}
	}
	result.CheckedAtUTC = s.currentTime().UTC()
	log.WithFields(map[string]any{
		"fixed_port": DirectExposurePort, "candidate_count": result.CandidateCount,
		"dial_attempt_count": result.DialAttemptCount, "reachable_count": result.ReachableCount,
		"inconclusive_count": result.InconclusiveCount,
	}).Info("固定端口直连暴露观察完成")
	return result, nil
}

// CountActiveCollectors 从 collector 实际快照计算正在运行的数量。
//
// 参数：
//   - collectors: collector.Manager.List 返回的实际运行态快照
//
// 返回：
//   - StatusRunning 条目数，与 managed desired 清单无关
func CountActiveCollectors(collectors []model.Collector) int {
	count := 0
	for _, item := range collectors {
		if item.Status == model.StatusRunning {
			count++
		}
	}
	return count
}

func (s *Service) dial(ctx context.Context, address string) (net.Conn, error) {
	return s.dialContext(ctx, "tcp", address)
}

func (s *Service) effectiveDialTimeout() time.Duration {
	if s.dialTimeout > 0 {
		return s.dialTimeout
	}
	return defaultDialTimeout
}

func (s *Service) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func literalIPCandidates(values ...string) []string {
	out := make([]string, 0, len(values))
	seen := map[netip.Addr]struct{}{}
	for _, value := range values {
		addr, err := netip.ParseAddr(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		addr = addr.Unmap()
		// Host 地址允许公网或私网单播 IP；拒绝 loopback、link-local、
		// unspecified、multicast/broadcast，防止误探本机或 metadata 端点。
		if !addr.IsGlobalUnicast() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
			continue
		}
		if _, exists := seen[addr]; exists {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr.String())
	}
	return out
}

func directDialCauseCode(err error) string {
	switch {
	case err == nil:
		return "nil_connection"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "context_canceled"
	default:
		return "dial_failed"
	}
}
