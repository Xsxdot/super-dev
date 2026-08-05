// remote_command.rs 实现指向远端机器的 CommandRunner 端口。
//
// 职责：
//   - 把一次 CommandSpec 翻译成本机 agent 的 integrations 代理请求
//     （POST /api/agents/{host_id}/integrations/exec），并把响应还原成 CommandOutput
//   - 把 HTTP 层失败（连不上、非 2xx、响应无法解析）映射成稳定 ConnectorError 码
//   - 把目标机 agent 报告的 timed_out=true 映射为与本机 SystemCommandRunner
//     一致的 command_timeout 错误（CommandOutput 本身没有 timed_out 字段）
//
// 边界：
//   - 不知道任何连接器方言：argv 由调用方（openclaw.rs / grok.rs）拼好
//   - 不做白名单判断——那是 agent 侧 integrations_exec_allowlist.go 的职责，
//     客户端自觉不构成安全边界
//   - local_token 只出现在 Authorization 头里，**绝不进日志或错误串**
//   - 只送命令名不送路径：桌面机上的绝对路径在目标机上毫无意义，解析是 agent 的事

use crate::mcp_install::command_port::{
    CommandOutput, CommandRunner, CommandSpec, MAX_CAPTURED_BYTES,
};
use crate::mcp_install::registry::ConnectorError;
use std::time::Duration;

/// REMOTE_COMMAND_HTTP_TIMEOUT 是本机 → agent 这一跳的 HTTP 超时。
///
/// 比命令自身的时限宽裕：命令超时由目标机 agent 判定并以 timed_out=true 正常
/// 返回，HTTP 层不该先于它失败，否则调用方分不清「命令超时」和「网络断了」。
/// 命令时限上限 60s，这里取 90s 留出代理转发与杀进程的余量。
const REMOTE_COMMAND_HTTP_TIMEOUT: Duration = Duration::from_secs(90);

/// RemoteAgentCommandRunner 把连接器的进程调用送到远端机器执行。
///
/// 数据流：桌面端 Rust（本结构） → HTTP 到【本机】agent
/// `/api/agents/{host_id}/integrations/exec`（Task 5 代理）→ 按 host_id 转发到
/// 【目标机】agent `POST /api/integrations/exec`。本结构只跟本机 agent 说话。
pub struct RemoteAgentCommandRunner {
    /// local_agent_base 是本机 agent 的完整 origin（如 "http://127.0.0.1:57017"）。
    local_agent_base: String,
    /// local_token 是本机 agent 签发的 local access token，只用于
    /// `Authorization: Bearer` 请求头——**绝不能**出现在日志或错误串里。
    local_token: String,
    /// host_id 是目标机器在本机 agent `agents.json` 中的注册 ID。
    host_id: String,
    /// agent 是复用连接池、统一配置 90s 超时的 ureq HTTP 客户端。
    agent: ureq::Agent,
}

impl RemoteAgentCommandRunner {
    /// new 构造一个指向 host_id 的远端命令端口。
    ///
    /// 参数：
    ///   - local_agent_base: 本机 agent 的完整 origin，例如 "http://127.0.0.1:57017"
    ///   - local_token: 本机 agent 的 local access token，仅用于鉴权本机代理调用
    ///   - host_id: 目标机器在本机 agent 里的注册 ID
    ///
    /// 返回：
    ///   - 已配置 90s HTTP 超时的 runner 实例
    pub fn new(local_agent_base: String, local_token: String, host_id: String) -> Self {
        tracing::debug!(
            host_id = %host_id,
            local_agent_base = %local_agent_base,
            "constructing RemoteAgentCommandRunner"
        );
        Self {
            local_agent_base,
            local_token,
            host_id,
            agent: ureq::AgentBuilder::new()
                .timeout(REMOTE_COMMAND_HTTP_TIMEOUT)
                .build(),
        }
    }

    /// exec_url 拼出到本机 agent 的完整 URL：
    /// `{base}/api/agents/{host_id}/integrations/exec`。
    fn exec_url(&self) -> String {
        format!(
            "{}/api/agents/{}/integrations/exec",
            self.local_agent_base.trim_end_matches('/'),
            self.host_id
        )
    }
}

/// program_name 从 CommandSpec 的 program 里取出命令名（basename）。
///
/// 参数：
///   - spec: 调用规格
///
/// 返回：
///   - 文件名部分；取不到时退化为整串（调用方本来就传的是裸命令名）
///
/// 注意：
///   - 桌面机上的绝对路径在目标机上毫无意义；agent 负责按白名单目录解析可执行文件
fn program_name(spec: &CommandSpec) -> String {
    spec.program
        .file_name()
        .map(|name| name.to_string_lossy().into_owned())
        .unwrap_or_else(|| spec.program.to_string_lossy().into_owned())
}

/// truncate 把远端返回的输出截断到与本机实现一致的上限（MAX_CAPTURED_BYTES）。
///
/// 参数：
///   - raw: 远端返回的 stdout 或 stderr 原文
///
/// 返回：
///   - 截断后的文本；若原文字节长度未超限则原样拷贝
///   - 是否发生了截断（用于填充 CommandOutput.truncated）
///
/// 注意：
///   - 按 UTF-8 字符边界截断，避免在多字节字符中间切开
///   - 与 SystemCommandRunner 的字节上限一致，保证本机/远端语义对齐
fn truncate(raw: &str) -> (String, bool) {
    if raw.len() <= MAX_CAPTURED_BYTES {
        return (raw.to_string(), false);
    }
    let text: String = raw
        .chars()
        .scan(0usize, |used, ch| {
            *used += ch.len_utf8();
            (*used <= MAX_CAPTURED_BYTES).then_some(ch)
        })
        .collect();
    (text, true)
}

impl CommandRunner for RemoteAgentCommandRunner {
    /// run 把调用送到目标机执行并还原输出。
    ///
    /// 参数：
    ///   - spec: argv 级进程调用规格（program 只取 basename 下发）
    ///
    /// 返回：
    ///   - Ok(CommandOutput)：进程已结束（含非零退出码）；stdout/stderr 已有界截断
    ///   - Err：HTTP/代理/白名单拒绝，或目标机报告命令超时（command_timeout）
    ///
    /// 注意：
    ///   - 非零退出码是**正常返回值**，不是错误：连接器方言层（如 openclaw 的
    ///     show → set → show 复核）要靠 exit_code 与 stderr 自己判断
    ///   - 响应体 `timed_out: true` 与本机超时对齐，返回 `command_timeout`，
    ///     不塞进 CommandOutput（该结构只有 truncated，没有 timed_out）
    ///   - 日志与错误串不写 local_token、不写 argv 内容
    fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError> {
        let program = program_name(&spec);
        let arg_count = spec.args.len();
        let env: serde_json::Map<String, serde_json::Value> = spec
            .env
            .iter()
            .map(|(key, value)| {
                (
                    key.to_string_lossy().into_owned(),
                    serde_json::Value::String(value.to_string_lossy().into_owned()),
                )
            })
            .collect();
        let body = serde_json::json!({
            "program": &program,
            "args": spec
                .args
                .iter()
                .map(|arg| arg.to_string_lossy().into_owned())
                .collect::<Vec<_>>(),
            "env": env,
            "timeout_ms": spec.timeout.as_millis() as u64,
        });

        tracing::info!(
            host_id = %self.host_id,
            program = program.as_str(),
            arg_count,
            "remote connector command starting"
        );

        let response = self
            .agent
            .post(&self.exec_url())
            .set("Authorization", &format!("Bearer {}", self.local_token))
            .send_json(body);

        match response {
            Ok(resp) => {
                let text = resp.into_string().map_err(|error| {
                    tracing::error!(
                        host_id = %self.host_id,
                        program = program.as_str(),
                        error_code = "remote_command_unreadable",
                        "remote command response body unreadable"
                    );
                    ConnectorError::new(
                        "remote_command_unreadable",
                        format!("远端命令响应无法读取: {error}"),
                    )
                })?;
                let value: serde_json::Value = serde_json::from_str(&text).map_err(|error| {
                    tracing::error!(
                        host_id = %self.host_id,
                        program = program.as_str(),
                        error_code = "remote_command_invalid_response",
                        "remote command response unparsable"
                    );
                    ConnectorError::new(
                        "remote_command_invalid_response",
                        format!("远端命令响应无法解析: {error}"),
                    )
                })?;

                // 与本机 SystemCommandRunner 超时路径对齐：超时是 Err，不是带
                // timed_out 字段的 Ok。CommandOutput 只有 truncated，没有 timed_out。
                if value["timed_out"].as_bool().unwrap_or(false) {
                    tracing::error!(
                        host_id = %self.host_id,
                        program = program.as_str(),
                        error_code = "command_timeout",
                        "remote connector command timed out"
                    );
                    return Err(ConnectorError::new("command_timeout", "命令执行超时"));
                }

                let status_code = value["exit_code"].as_i64().map(|code| code as i32);
                let (stdout, stdout_truncated) =
                    truncate(value["stdout"].as_str().unwrap_or_default());
                let (stderr, stderr_truncated) =
                    truncate(value["stderr"].as_str().unwrap_or_default());
                let truncated = stdout_truncated || stderr_truncated;
                let output = CommandOutput {
                    status_code,
                    stdout,
                    stderr,
                    truncated,
                };

                if output.success() {
                    tracing::info!(
                        host_id = %self.host_id,
                        program = program.as_str(),
                        status_code = output.status_code,
                        truncated,
                        "remote connector command finished"
                    );
                } else {
                    tracing::warn!(
                        host_id = %self.host_id,
                        program = program.as_str(),
                        status_code = output.status_code,
                        truncated,
                        "remote connector command exited nonzero"
                    );
                }
                Ok(output)
            }
            Err(ureq::Error::Status(status, resp)) => {
                // 只取稳定 code，不把服务端 message 拼进错误串——那可能含路径。
                let code = resp
                    .into_string()
                    .ok()
                    .and_then(|text| serde_json::from_str::<serde_json::Value>(&text).ok())
                    .and_then(|value| value["code"].as_str().map(str::to_string))
                    .unwrap_or_else(|| "unknown".to_string());
                tracing::error!(
                    host_id = %self.host_id,
                    program = program.as_str(),
                    status,
                    reject_code = code.as_str(),
                    error_code = "remote_command_rejected",
                    "remote connector command rejected"
                );
                Err(ConnectorError::new(
                    "remote_command_rejected",
                    format!("远端机器拒绝执行该命令（HTTP {status}，原因 {code}）"),
                ))
            }
            Err(error) => {
                // 传输层错误对象里不应含 token（token 只在 Authorization 头）；
                // 仍避免把整段 error 原样写进面向用户的路径外日志字段以外的地方时泄密。
                tracing::error!(
                    host_id = %self.host_id,
                    program = program.as_str(),
                    error_code = "remote_command_unreachable",
                    "remote connector command transport failed"
                );
                Err(ConnectorError::new(
                    "remote_command_unreachable",
                    format!("无法连接本机 agent 执行远端命令: {error}"),
                ))
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::net::TcpListener;
    use std::thread;

    /// serve_once 起一个只应答一次的极简 HTTP server，返回它的 origin 与
    /// 收到的请求体。测试要断言的是「桌面端发出去的 JSON 长什么样」。
    fn serve_once(status: u16, body: &'static str) -> (String, std::sync::mpsc::Receiver<String>) {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind");
        let origin = format!("http://{}", listener.local_addr().expect("addr"));
        let (tx, rx) = std::sync::mpsc::channel();
        thread::spawn(move || {
            let (mut stream, _) = listener.accept().expect("accept");
            // 按 Content-Length 读满 body，避免单次 read 截断大请求或半包。
            let mut header_buf = Vec::new();
            let mut byte = [0u8; 1];
            loop {
                stream.read_exact(&mut byte).expect("read header byte");
                header_buf.push(byte[0]);
                if header_buf.ends_with(b"\r\n\r\n") {
                    break;
                }
                // 防护：异常客户端不会无限撑大缓冲
                if header_buf.len() > 64 * 1024 {
                    break;
                }
            }
            let header_text = String::from_utf8_lossy(&header_buf);
            let content_length: usize = header_text
                .lines()
                .find_map(|line| {
                    let lower = line.to_ascii_lowercase();
                    lower
                        .strip_prefix("content-length:")
                        .and_then(|v| v.trim().parse().ok())
                })
                .unwrap_or(0);
            let mut body_buf = vec![0u8; content_length];
            if content_length > 0 {
                stream.read_exact(&mut body_buf).expect("read body");
            }
            let request_body = String::from_utf8_lossy(&body_buf).into_owned();
            let _ = tx.send(request_body);
            let response = format!(
                "HTTP/1.1 {status} OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
                body.len()
            );
            let _ = stream.write_all(response.as_bytes());
        });
        (origin, rx)
    }

    #[test]
    fn run_sends_command_name_not_local_path() {
        let (origin, rx) = serve_once(
            200,
            r#"{"exit_code":0,"stdout":"ok","stderr":"","timed_out":false}"#,
        );
        let runner = RemoteAgentCommandRunner::new(origin, "tok".into(), "h1".into());
        let spec = CommandSpec::new(
            std::path::PathBuf::from("/opt/homebrew/bin/openclaw"),
            ["mcp", "set", "superdev", "{}"],
        );

        let output = runner.run(spec).expect("run");

        assert_eq!(output.stdout, "ok");
        assert!(!output.truncated);
        let sent = rx.recv().expect("request body");
        let value: serde_json::Value = serde_json::from_str(&sent).expect("json");
        assert_eq!(
            value["program"], "openclaw",
            "必须只送命令名：解析目标机上的绝对路径是 agent 的职责，桌面机的路径在目标机上毫无意义"
        );
        assert_eq!(
            value["args"],
            serde_json::json!(["mcp", "set", "superdev", "{}"])
        );
    }

    #[test]
    fn run_forwards_whitelisted_env() {
        let (origin, rx) = serve_once(
            200,
            r#"{"exit_code":0,"stdout":"","stderr":"","timed_out":false}"#,
        );
        let runner = RemoteAgentCommandRunner::new(origin, "tok".into(), "h1".into());
        let spec = CommandSpec::new(std::path::PathBuf::from("openclaw"), ["mcp", "show"])
            .with_env("OPENCLAW_CONFIG_PATH", "/home/u/.openclaw/openclaw.json");

        runner.run(spec).expect("run");

        let value: serde_json::Value =
            serde_json::from_str(&rx.recv().expect("body")).expect("json");
        assert_eq!(
            value["env"]["OPENCLAW_CONFIG_PATH"],
            "/home/u/.openclaw/openclaw.json"
        );
        // timeout_ms 必须从 spec 带过去，供目标机杀进程。
        assert!(value["timeout_ms"].as_u64().unwrap_or(0) > 0);
    }

    #[test]
    fn run_returns_non_zero_exit_as_output_not_error() {
        let (origin, _rx) = serve_once(
            200,
            r#"{"exit_code":3,"stdout":"","stderr":"boom","timed_out":false}"#,
        );
        let runner = RemoteAgentCommandRunner::new(origin, "tok".into(), "h1".into());

        let output = runner
            .run(CommandSpec::new(
                std::path::PathBuf::from("grok"),
                ["mcp", "list"],
            ))
            .expect("非零退出是正常返回值，连接器方言层要自己判断");

        assert_eq!(output.status_code, Some(3));
        assert_eq!(output.stderr, "boom");
        assert!(!output.success());
        assert!(!output.truncated);
    }

    #[test]
    fn run_maps_forbidden_to_stable_error_code() {
        let (origin, _rx) = serve_once(
            403,
            r#"{"code":"program_not_allowed","error":"command not allowed"}"#,
        );
        let runner = RemoteAgentCommandRunner::new(origin, "tok-secret-xyz".into(), "h1".into());

        let error = runner
            .run(CommandSpec::new(
                std::path::PathBuf::from("bash"),
                ["mcp"],
            ))
            .expect_err("白名单拒绝必须是错误");

        assert_eq!(error.code(), "remote_command_rejected");
        assert!(
            !error.message().contains("tok-secret-xyz"),
            "错误串绝不能带上 token"
        );
        let debug = format!("{error:?}");
        assert!(!debug.contains("tok-secret-xyz"));
    }

    #[test]
    fn run_truncates_oversized_output() {
        let long = "x".repeat(MAX_CAPTURED_BYTES + 100);
        let body: &'static str = Box::leak(
            format!(r#"{{"exit_code":0,"stdout":"{long}","stderr":"","timed_out":false}}"#)
                .into_boxed_str(),
        );
        let (origin, _rx) = serve_once(200, body);
        let runner = RemoteAgentCommandRunner::new(origin, "tok".into(), "h1".into());

        let output = runner
            .run(CommandSpec::new(
                std::path::PathBuf::from("grok"),
                ["mcp"],
            ))
            .expect("run");

        assert_eq!(output.stdout.len(), MAX_CAPTURED_BYTES);
        assert!(
            output.truncated,
            "超限 stdout 必须把 CommandOutput.truncated 置 true"
        );
    }

    #[test]
    fn run_maps_timed_out_to_command_timeout_error() {
        // 与本机 SystemCommandRunner 超时路径对齐：agent 报告 timed_out=true
        // 时返回 Err(command_timeout)，而不是 Ok(CommandOutput { timed_out: ... })
        // ——CommandOutput 根本没有 timed_out 字段，只有 truncated。
        let (origin, _rx) = serve_once(
            200,
            r#"{"exit_code":null,"stdout":"","stderr":"","timed_out":true}"#,
        );
        let runner = RemoteAgentCommandRunner::new(origin, "tok".into(), "h1".into());

        let error = runner
            .run(CommandSpec::new(
                std::path::PathBuf::from("openclaw"),
                ["mcp", "show"],
            ))
            .expect_err("timed_out 必须映射为错误");

        assert_eq!(error.code(), "command_timeout");
        assert_eq!(error.message(), "命令执行超时");
    }
}
