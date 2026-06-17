//go:build windows

// pidresolve_windows.go 用 Win32_Process 枚举进程树，对等 Unix 的 ps。
//
// 职责：
//   - 读取 ProcessId/ParentProcessId/Name
//   - 以 deployment 主 pid 为根，递归返回子进程树
//
// 边界：
//   - Windows 无 pgid 概念，入参 pgid 在此解释为主进程 pid
//   - 不终止或附加任何进程，只为 attach 解析提供候选列表
package codedebug

import (
	"encoding/csv"
	"log"
	"os/exec"
	"strconv"
	"strings"
)

type windowsProcInfo struct {
	pid  int
	ppid int
	comm string
}

// listProcessGroupOS 返回主 pid 及其子进程树的进程列表。
func listProcessGroupOS(pgid int) []procInfo {
	if pgid <= 0 {
		return nil
	}
	out, err := exec.Command(
		"powershell",
		"-NoProfile",
		"-Command",
		"Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name | ConvertTo-Csv -NoTypeInformation",
	).Output()
	if err != nil {
		log.Printf("[codedebug] list windows process tree failed root=%d: %v", pgid, err)
		return nil
	}

	rows, err := csv.NewReader(strings.NewReader(string(out))).ReadAll()
	if err != nil || len(rows) == 0 {
		log.Printf("[codedebug] parse windows process tree failed root=%d rows=%d err=%v", pgid, len(rows), err)
		return nil
	}
	header := csvHeaderIndex(rows[0])
	pidIdx, pidOK := header["processid"]
	ppidIdx, ppidOK := header["parentprocessid"]
	nameIdx, nameOK := header["name"]
	if !pidOK || !ppidOK || !nameOK {
		log.Printf("[codedebug] windows process tree missing columns root=%d header=%v", pgid, rows[0])
		return nil
	}

	byPID := map[int]windowsProcInfo{}
	children := map[int][]windowsProcInfo{}
	for _, row := range rows[1:] {
		if len(row) <= pidIdx || len(row) <= ppidIdx || len(row) <= nameIdx {
			continue
		}
		pid, pidErr := strconv.Atoi(strings.TrimSpace(row[pidIdx]))
		ppid, ppidErr := strconv.Atoi(strings.TrimSpace(row[ppidIdx]))
		if pidErr != nil || ppidErr != nil || pid <= 0 {
			continue
		}
		info := windowsProcInfo{pid: pid, ppid: ppid, comm: strings.TrimSpace(row[nameIdx])}
		byPID[pid] = info
		children[ppid] = append(children[ppid], info)
	}

	var procs []procInfo
	seen := map[int]bool{}
	queue := []int{pgid}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if p, ok := byPID[pid]; ok {
			procs = append(procs, procInfo{pid: p.pid, comm: p.comm})
		}
		for _, child := range children[pid] {
			queue = append(queue, child.pid)
		}
	}
	log.Printf("[codedebug] listed windows process tree root=%d count=%d", pgid, len(procs))
	return procs
}

func csvHeaderIndex(header []string) map[string]int {
	idx := map[string]int{}
	for i, name := range header {
		idx[strings.ToLower(strings.TrimSpace(name))] = i
	}
	return idx
}
