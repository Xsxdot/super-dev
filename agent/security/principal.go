// principal.go 定义「已验证凭据 -> 请求主体」的唯一映射。
//
// 职责：
//   - 定义 Principal：一次请求携带的最小身份信息（凭据类别/ID/展示名）
//   - 提供 WithPrincipal/PrincipalFrom，把 Principal 挂进/取出 context.Context，
//     供中间件下游（如审批裁决、审计日志）读取「这次请求代表谁」
//
// 边界：
//   - 只做映射，不做任何授权/权限判定——Principal 不回答「能不能做」，只回答「是谁」
//   - 不负责验证凭据本身，校验逻辑在 Store.VerifyToken*/VerifyLocalToken
//   - 不持久化、不跨请求传递——生命周期严格绑定单次 HTTP 请求的 context
package security

import "context"

// PrincipalType 标识请求主体的凭据来源类别。
type PrincipalType string

const (
	// PrincipalLocal 表示凭据来自本机 local-access-token（本机桌面/MCP/CLI 使用）。
	PrincipalLocal PrincipalType = "local"
	// PrincipalRemote 表示凭据命中某条已 provision/追加的 TokenRecord（远程控制面）。
	PrincipalRemote PrincipalType = "remote"
)

// Principal 是「已验证凭据」推导出的请求主体。
//
// 用途：多控制面场景下需要区分「哪个控制面在发起请求」（如审批裁决审计需要
// 记录真实裁决方，而非请求体自报的 decided_by）；Principal 就是承载这个身份的
// 结构，只在鉴权命中后由 withSecurity 注入，不参与鉴权判定本身。
type Principal struct {
	// Type 标识主体类别：local 或 remote。
	Type PrincipalType
	// ID 是主体的稳定标识：remote 为命中的 TokenRecord.ID；local 固定为 "local"。
	ID string
	// Name 是主体的展示名：remote 为命中的 TokenRecord.Name；local 固定为「本机」。
	Name string
}

// principalContextKey 是 Principal 在 context.Context 中的键类型。
//
// 用未导出的空结构体类型而非字符串常量做键，避免跨包 context key 碰撞。
type principalContextKey struct{}

// WithPrincipal 把 Principal 挂进 context，返回携带它的新 context。
//
// 调用方：仅 withSecurity 中间件在鉴权命中后调用一次；不应在业务 handler 内
// 再次调用覆盖——Principal 代表「凭据校验的结论」，业务层只应读取，不应改写。
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// PrincipalFrom 从 context 中取出 Principal。
//
// 返回：
//   - 命中时返回 Principal 与 true
//   - 未注入时返回零值与 false——bypass 白名单路径（无凭据校验）不会注入 Principal，
//     调用方必须显式处理「无主体」分支，不能假设 ctx 里恒有值。
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}
