// project_home_routing.go 实现「归属路由」——项目 dev 环境归属到另一台节点
// 之后，把该项目的 dev 运行控制与配置编辑请求原样转发到归属 agent 的同路径
// 端点，在归属节点复刻本机 location:local 的处理语义。
//
// 职责：
//   - forwardToHome：把当前 HTTP 请求原样转发给归属 agent，回写其响应
//   - 提供两套「是否需要转发」判定：
//     1. deployment 运行控制 / startEnvSelected：区分 dev/prod env，只转发
//     dev 环境的请求
//     2. 项目配置读写（get/preview/apply）：不区分 env，整份 project.yaml
//     归属节点持有
//   - 在白名单接入点（handler_deployments.go 的 controlDeploymentRuntime、
//     handler_projects.go 的 startEnvSelected/putEnvSelected、
//     handler_config_changes.go 的
//     getProjectConfig/previewConfigChange/applyConfigChange）逐个显式调用
//     上述判定，在本机 authorizeOperation 之前短路转发。putEnvSelected 与
//     startEnvSelected 成对接入：前者写"选中了哪些服务"，后者读它来决定
//     启动谁，两者必须转发到同一台机器，否则会出现"这台机器上选的是
//     A/B，归属机上实际起的却是 C"的错配
//
// 边界：
//   - 不做任何通配中间件转发。为什么白名单逐个接不做通配：路由是安全敏感
//     行为——它决定一次写操作最终落在哪台机器上执行。必须可枚举、可审计、
//     可逐点测试；一条隐式的"匹配某种模式就转发"规则一旦覆盖面判断错误，
//     后果是在错误的机器上执行了未经审查的操作，且没有任何一处调用点可供
//     审查者按图索骥确认"这条路由到底生不生效"。
//   - 转发失败（host 不可达、传输层错误）绝不静默回落本机执行。为什么不
//     回落本机：回落等于在错误的机器上启动进程，或者把编辑写进本机那份
//     转移之后就不再是权威版本的 project.yaml 副本——这比直接返回一个
//     明确的 502 错误更危险，错误必须显式暴露给调用方处理，不能被"尽力
//     而为"的降级逻辑悄悄吞掉。
//   - 转发只发生在本机 authorizeOperation 之前：归属机收到转发请求后会
//     按它自己的安全策略重新裁决（dev local 免审批），语义与"请求方直接
//     对归属机发起操作"完全一致；本机不需要、也不应该对一个即将转发出去
//     的操作先审批一次。
package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/xsxdot/super-dev/agent/configchange"
	"github.com/xsxdot/super-dev/agent/model"
	"github.com/xsxdot/super-dev/agent/nodetransport"
)

// homeRouteForwardedHeaders 是转发到归属机时逐个放行的请求头白名单，取值与
// CORS 允许头一致（middleware_cors.go）：Content-Type/Authorization 是请求
// 正常处理所需，三个 X-SuperDev-* 头供归属机自己的 authorizeOperation/
// 操作审计记录发起者与豁免 token。不整体透传 r.Header——避免把本机专属的
// 头意外泄漏给归属机，也让转发的头面保持可枚举。
var homeRouteForwardedHeaders = []string{
	"Content-Type",
	"Authorization",
	"X-SuperDev-Requester",
	"X-SuperDev-Requester-Label",
	"X-SuperDev-Approval-Token",
}

// homeRouteTargetForDeployment 返回 deployment 运行控制类请求（start/stop/
// restart、startEnvSelected）应转发到的归属主机 ID；空串表示本机处理。
//
// 参数：
//   - project: deployment 所属项目（用于查归属 + dev 环境声明）
//   - envName: 该 deployment（或该 start-selected 请求）所属的 EnvName
//
// 注意：
//   - 只有 envName 属于该项目声明的 dev 环境时才可能转发——prod 部署的
//     host 由其自身 Location/HostIDs 钉死，不随项目归属移动（spec 裁定：
//     归属只描述"dev 环境在哪个节点上跑"，不改变已经配置好的 prod 拓扑）。
//     复用 project_transfer_engine.go 的 devEnvSet，与转移引擎判断
//     "运行中 dev 部署" 用同一套 dev 环境口径，不重新定义。
func (a *App) homeRouteTargetForDeployment(project model.Project, envName string) string {
	home := a.projectHomeOf(project.ID)
	if home == "" {
		return ""
	}
	if !devEnvSet(project)[envName] {
		return ""
	}
	return home
}

// homeRouteTargetForProject 返回项目级请求（配置读写，无 env 语义）应转发到
// 的归属主机 ID；空串表示本机处理。
//
// 不做 dev/prod 过滤：config get/preview/apply 操作的是整份 project.yaml
// （dev 与 prod 环境共用同一份文件），归属节点是这份文件的实际落盘位置，
// 本机保留的旧副本转移之后即视为过期镜像，读写都必须去归属节点，与该项目
// 下具体哪个 env 无关。
func (a *App) homeRouteTargetForProject(projectID string) string {
	return a.projectHomeOf(projectID)
}

// homeRouteTargetForChangeRequest 从 config-changes 请求体解析出目标项目
// （复用 resolveConfigChangeProject 的 ID/Name/RootPath 匹配惯例，不重新
// 实现一遍匹配逻辑），若该项目已有非本机归属则返回归属主机 ID。
//
// 请求体不含可匹配的已知项目时（典型场景：POST /api/config-changes/apply
// 创建一个全新项目）返回空串——全新项目此刻在 projectHomeStore 里还没有
// 任何归属记录，天然留在本机处理，这也是"从零创建项目配置"必须始终可用
// 的唯一入口，不能被误判转发到任何地方。
func (a *App) homeRouteTargetForChangeRequest(r *http.Request) string {
	var req configchange.ChangeRequest
	if err := decodeJSONPreserveBody(r, &req); err != nil {
		return ""
	}
	project, status, _ := a.resolveConfigChangeProject(req)
	if status != http.StatusOK {
		return ""
	}
	return a.homeRouteTargetForProject(project.ID)
}

// projectHomeOf 是 projectHomeStore.HomeOf 的防御性包装：store 未装配、
// projectID 为空、或归属恰好等于本机 identity.NodeID 时一律返回空串（视为
// 本机处理）。归属存储正常情况下不会写入本机自身 ID（SetHome 只在转移到
// "另一台" host 时调用），这里的自身 ID 兜底纯粹是防御性的，避免任何异常
// 写入导致请求转发给自己、形成死循环。
func (a *App) projectHomeOf(projectID string) string {
	if a.projectHomeStore == nil || projectID == "" {
		return ""
	}
	home := a.projectHomeStore.HomeOf(projectID)
	if home == "" || home == a.identity.NodeID {
		return ""
	}
	return home
}

// forwardToHome 把当前请求原样转发到归属 agent 的同路径端点，并将其响应
// 原样回写给调用方。
//
// 参数：
//   - w/r: 原始 HTTP 请求/响应；调用后 r.Body 会被完整读取一次
//   - homeHostID: 目标归属主机 ID，调用方必须先经 homeRouteTargetFor* 系列
//     判定非空（即"确实需要转发"）才能调用本函数
//
// 注意：
//   - 转发失败（host 不可达、传输层错误）一律回 502 + 稳定错误码
//     home_unreachable，绝不静默回落本机执行——见文件头「为什么不回落
//     本机」。
//   - 成功转发后原样回写归属 agent 的状态码与响应体；调用方在调用本函数
//     之后必须立即 return，不得再向 w 写入任何内容。
func (a *App) forwardToHome(w http.ResponseWriter, r *http.Request, homeHostID string) {
	var body []byte
	if r.Body != nil {
		read, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("[SuperDev] homeroute: 读取请求体失败 %s %s → %s err=%v", r.Method, r.URL.Path, homeHostID, err)
			jsonErrorCode(w, http.StatusBadGateway, "home_unreachable", "failed to read request body: "+err.Error(), nil)
			return
		}
		body = read
	}

	headers := http.Header{}
	for _, key := range homeRouteForwardedHeaders {
		if v := r.Header.Get(key); v != "" {
			headers.Set(key, v)
		}
	}

	resp, err := a.nodeTransportDo()(r.Context(), homeHostID, nodetransport.NodeRequest{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: headers,
		Body:    bytes.NewReader(body),
	})
	if err != nil {
		log.Printf("[SuperDev] homeroute: 转发失败 %s %s → %s err=%v", r.Method, r.URL.Path, homeHostID, err)
		jsonErrorCode(w, http.StatusBadGateway, "home_unreachable", "home host unreachable: "+err.Error(), map[string]string{"host_id": homeHostID})
		return
	}
	if resp.Body != nil {
		defer resp.Body.Close()
	}

	log.Printf("[SuperDev] homeroute: %s %s → %s", r.Method, r.URL.Path, homeHostID)

	if resp.Headers != nil {
		if ct := resp.Headers.Get("Content-Type"); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
	}
	status := resp.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if resp.Body != nil {
		if _, err := io.Copy(w, resp.Body); err != nil {
			log.Printf("[SuperDev] homeroute: 回写响应体失败 %s %s → %s err=%v", r.Method, r.URL.Path, homeHostID, err)
		}
	}
}

// nodeTransportDo 返回本次转发使用的 nodetransport Do。a.nodeTransport 正常
// 由 NewApp 装配为真实 dispatcher；这里只防御裸构造 *App（跳过 NewApp）的
// 场景，避免 nil 解引用 panic，转而返回一个恒定报错的桩，走 forwardToHome
// 既有的 502 home_unreachable 处理路径。
func (a *App) nodeTransportDo() func(ctx context.Context, hostID string, req nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
	if a.nodeTransport == nil {
		return func(context.Context, string, nodetransport.NodeRequest) (nodetransport.NodeResponse, error) {
			return nodetransport.NodeResponse{}, fmt.Errorf("nodeTransport 未装配")
		}
	}
	return a.nodeTransport.Do
}
