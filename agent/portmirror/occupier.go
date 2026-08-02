// Package portmirror 提供端口镜像场景下"谁占住了这个本机端口"的单次身份识别。
//
// 职责：
//   - 用 lsof 识别监听 127.0.0.1:<port> 的进程（PID、进程名、启动时间）
//   - 通过调用方注入的 ManagedResolver 反查该 PID 是否是 SuperDev 托管进程，
//     命中则回填 ManagedDeploymentID，供上层决定处理动作走 stop_service 语义
//     还是纯展示
//
// 边界：
//   - 不杀进程、不轮询、不监听端口状态变化——每次调用只做一次快照式识别，
//     由调用方（manager）决定何时/是否重复调用
//   - lsof 不可用（未安装/非 unix 平台）或未找到监听者时一律返回 (nil, nil)，
//     不阻塞上层"端口已被占用"这个已确认的事实（该事实来自 tunnel.ErrLocalPortBusy），
//     只是占用者详情降级为 unknown，不当错误处理
//   - 不建立/拆除端口转发，那是 tunnel 包的职责；本包只读进程表
package portmirror

import (
	"bufio"
	"bytes"
	"errors"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Occupier 描述占住本机端口的进程——镜像冲突详情弹窗的数据来源。
type Occupier struct {
	PID       int       `json:"pid"`
	Name      string    `json:"name"`       // 进程名（lsof COMMAND 列）
	StartedAt time.Time `json:"started_at"` // 零值表示获取失败
	// ManagedDeploymentID 非空表示占用者是 SuperDev 托管进程——处理动作走 stop_service 语义。
	ManagedDeploymentID string `json:"managed_deployment_id,omitempty"`
}

// ManagedResolver 由调用方注入：pid → 托管 deploymentID。查不到返回 ("", false)。
type ManagedResolver func(pid int) (string, bool)

// LookupOccupier 识别监听 127.0.0.1:port 的进程。
// darwin/linux 用 lsof；不可用/无结果/非 unix 平台返回 (nil, nil)——
// 冲突照报，占用者详情降级为 unknown，不阻塞主链路。
func LookupOccupier(port int, resolve ManagedResolver) (*Occupier, error) {
	pid, name, err := runLsofListener(port)
	if err != nil {
		if isLsofUnavailable(err) {
			// lsof 命令本身跑不起来：未安装，或当前平台压根没有这个命令
			// （含非 unix 平台，如 windows）。这是环境问题，值得被看到，
			// 但只降级不中断——占用者详情降级为 unknown。Warn 语义、一次性。
			log.Printf("[SuperDev] portmirror: 端口 %d 占用者识别不可用: %v", port, err)
			return nil, nil
		}
		if isNoResultsExitCode(err) {
			// lsof 正常运行，只是没有匹配的监听者——这是「没人占用」最常见的
			// 表达方式，是正常路径而非异常，不打日志（避免高频探测时的噪音）。
			return nil, nil
		}
		// 其余情况（权限问题、参数错误等）是真实的意外错误，如实返回，
		// 由调用方决定如何呈现，不在本函数内吞掉。
		return nil, err
	}
	if pid == 0 {
		// lsof 有输出但没能解析出任何 p 字段——按无结果处理，不报错。
		return nil, nil
	}

	occ := &Occupier{PID: pid, Name: name, StartedAt: lookupStartTime(pid)}
	managed := false
	if resolve != nil {
		if depID, ok := resolve(pid); ok {
			occ.ManagedDeploymentID = depID
			managed = true
		}
	}

	log.Printf("[SuperDev] portmirror: 端口 %d 占用者 pid=%d name=%s managed=%v", port, occ.PID, occ.Name, managed)
	return occ, nil
}

// runLsofListener 执行 lsof 查询监听 127.0.0.1:port 的第一个进程，返回其 pid/name。
// err 非 nil 时由调用方分类：lsof 缺失、无结果都是可降级场景，其余视为真实错误。
func runLsofListener(port int) (pid int, name string, err error) {
	cmd := exec.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc")
	out, err := cmd.Output()
	if err != nil {
		return 0, "", err
	}
	pid, name, found := parseLsofFields(out)
	if !found {
		return 0, "", nil
	}
	return pid, name, nil
}

// parseLsofFields 解析 `lsof -Fpc` 的字段输出。
//
// lsof -F 格式：每行以单字符字段标识开头，字符后紧跟内容，行末换行结束。
// p<pid> 开启一条新的进程记录；c<name> 是该进程的命令名。p/f 两个字段
// lsof 总会输出（man lsof: "always selected"），即便只请求了 -Fpc 也会
// 额外出现 f<fd> 这类未请求的文件集字段——逐行扫描只认 p/c 前缀，未知
// 前缀直接跳过，天然兼容这类多余字段，不会解析失败。
//
// 只取第一个监听者：同一端口多进程监听（SO_REUSEPORT）场景极其罕见，
// v1 不展开成列表，遇到第二条记录的起始 p 行就立即返回已收集到的第一条。
// 如果只拿到 pid 没拿到 name（极端情况下 c 行缺失），仍返回该记录、
// Name 留空，而不是当作解析失败丢弃——占用者身份（pid）比进程名更关键。
func parseLsofFields(out []byte) (pid int, name string, found bool) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			if found {
				// 第二条监听者记录的开始——第一条已收集完整，就此打住。
				return pid, name, true
			}
			parsedPID, convErr := strconv.Atoi(line[1:])
			if convErr != nil {
				continue
			}
			pid = parsedPID
			found = true
		case 'c':
			if found && name == "" {
				name = line[1:]
			}
		}
	}
	return pid, name, found
}

// isLsofUnavailable 判断 err 是否代表 lsof 命令本身跑不起来
// （未安装，或当前平台没有这个命令——含非 unix 平台）。
func isLsofUnavailable(err error) bool {
	return errors.Is(err, exec.ErrNotFound)
}

// isNoResultsExitCode 判断 err 是否是 lsof 因为没有匹配结果而返回的退出码 1——
// 这是「端口没人监听」的正常表达方式，不是错误。
func isNoResultsExitCode(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

// lookupStartTime 用 `ps -o lstart=` 取进程启动时间。解析失败（含 ps 本身失败，
// 例如进程已退出）时返回零值，不当作错误处理——启动时间只是展示性字段，
// 缺失不影响占用者身份（pid/name）的确定性，不应该让整次识别失败。
func lookupStartTime(pid int) time.Time {
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return time.Time{}
	}
	raw := strings.TrimSpace(string(out))
	// macOS/Linux 的 ps lstart 形如 "Sat Aug  2 11:20:03 2026"（本地时区、无 offset），
	// 与 time.ANSIC 参考布局完全一致；ParseInLocation 按本机时区还原真实时刻，
	// 而不是把本地时间的时钟数字错当成 UTC。
	t, err := time.ParseInLocation(time.ANSIC, raw, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}
