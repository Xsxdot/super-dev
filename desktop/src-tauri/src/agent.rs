use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::PathBuf;
use std::process::Command;
use std::sync::Mutex;
use std::thread::sleep;
use std::time::{Duration, Instant};

use crate::mcp_install::resolve_sidecar_binary;
use tauri::{AppHandle, Manager};
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

const REQUIRED_AGENT_ENDPOINTS: [&str; 4] = [
    "/api/hosts",
    "/api/tunnels",
    "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
    "/api/projects/__compat_probe__/ingress",
];
const AGENT_START_TIMEOUT: Duration = Duration::from_secs(5);
const AGENT_PROBE_TIMEOUT: Duration = Duration::from_millis(300);
const AGENT_PORT_RECOVERY_TIMEOUT: Duration = Duration::from_secs(2);

#[derive(Debug, PartialEq, Eq)]
enum ProbeOutcome {
    Compatible,
    Unreachable,
    InvalidResponse { endpoint: &'static str },
    Incompatible { endpoint: &'static str, status: u16 },
}

enum EndpointProbe {
    Status(u16),
    Unreachable,
    Invalid,
}

#[derive(Debug, PartialEq, Eq)]
enum AgentPortState {
    ReuseExisting,
    StartSidecar,
}

pub struct AgentProcess(pub Mutex<Option<CommandChild>>);

fn install_binaries_dir(app: &AppHandle) -> Option<PathBuf> {
    if let Ok(resource_dir) = app.path().resource_dir() {
        let bundled = resource_dir.join("agent-install");
        if has_install_binaries(&bundled) {
            return Some(bundled);
        }
    }
    if cfg!(debug_assertions) {
        let workspace = std::env::current_dir()
            .ok()?
            .join("src-tauri")
            .join("resources")
            .join("agent-install");
        if has_install_binaries(&workspace) {
            return Some(workspace);
        }
    }
    None
}

fn has_install_binaries(dir: &PathBuf) -> bool {
    if !dir.is_dir() {
        return false;
    }
    let Ok(entries) = std::fs::read_dir(dir) else {
        return false;
    };
    entries.filter_map(Result::ok).any(|entry| {
        entry
            .file_name()
            .to_str()
            .is_some_and(|name| name.starts_with("superdev-agent-"))
    })
}

impl AgentProcess {
    /// new 创建 AgentProcess 容器。
    ///
    /// 参数：无。
    ///
    /// 返回：持有可选 sidecar 子进程句柄的状态对象。
    ///
    /// 注意：实际 agent 进程只在 start 中启动。
    pub fn new() -> Self {
        AgentProcess(Mutex::new(None))
    }

    /// start 启动或复用本地 agent。
    ///
    /// 参数：
    ///   - app: Tauri AppHandle，用于解析并启动 sidecar。
    ///
    /// 返回：
    ///   - Ok 表示目标端口上的 agent 已兼容当前前端所需接口
    ///   - Err 表示 sidecar 缺失、启动失败，或端口被旧版 agent 占用
    ///
    /// 注意：
    ///   - dev 模式使用 57018，正式构建使用 57017
    ///   - 若端口已有兼容 agent，会直接复用，不接管其生命周期
    pub fn start(&self, app: &AppHandle) -> Result<(), String> {
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if guard.is_some() {
            return Ok(());
        }

        // debug_assertions 在 `tauri dev` 时为 true，`tauri build` 时为 false，
        // 以此区分开发版（57018）和正式版（57017），避免同时运行时端口冲突。
        let (addr, data_dir) = if cfg!(debug_assertions) {
            let home = std::env::var("HOME").unwrap_or_default();
            ("127.0.0.1:57018", format!("{home}/.superdev-dev"))
        } else {
            let home = std::env::var("HOME").unwrap_or_default();
            ("127.0.0.1:57017", format!("{home}/.superdev"))
        };

        match prepare_agent_port(
            addr,
            AGENT_PROBE_TIMEOUT,
            AGENT_PORT_RECOVERY_TIMEOUT,
            stop_incompatible_superdev_agent,
        )? {
            AgentPortState::ReuseExisting => {
                println!("[SuperDev] reuse existing agent on {addr}");
                return Ok(());
            }
            AgentPortState::StartSidecar => {}
        }

        let mut args = vec![
            "--addr".to_string(),
            addr.to_string(),
            "--data".to_string(),
            data_dir,
        ];
        if let Some(dir) = install_binaries_dir(app) {
            args.push("--install-binaries".to_string());
            args.push(dir.to_string_lossy().to_string());
        }
        if let Ok(sample) = resolve_sidecar_binary(app, "superdev-sample") {
            args.push("--sample-binary".to_string());
            args.push(sample.to_string_lossy().to_string());
        }

        let (_rx, child) = app
            .shell()
            .sidecar("superdev-agent")
            .map_err(|e| format!("找不到 agent sidecar: {e}"))?
            .args(args)
            .spawn()
            .map_err(|e| format!("启动 agent 失败: {e}"))?;

        if let Err(err) = wait_for_compatible_agent(addr, AGENT_START_TIMEOUT) {
            let _ = child.kill();
            return Err(err);
        }

        println!("[SuperDev] agent started");
        *guard = Some(child);
        Ok(())
    }

    /// stop 停止当前 Tauri 实例启动的 agent sidecar。
    ///
    /// 参数：无。
    ///
    /// 返回：无。
    ///
    /// 注意：复用已有兼容 agent 时不会保存 child 句柄，因此不会误杀外部进程。
    pub fn stop(&self) {
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(child) = guard.take() {
            let _ = child.kill();
            println!("[SuperDev] agent stopped");
        }
    }
}

fn wait_for_compatible_agent(addr: &str, timeout: Duration) -> Result<(), String> {
    let deadline = Instant::now() + timeout;
    loop {
        match probe_required_endpoints(addr, AGENT_PROBE_TIMEOUT) {
            ProbeOutcome::Compatible => return Ok(()),
            ProbeOutcome::Unreachable => {
                if Instant::now() >= deadline {
                    return Err(format!("agent 启动超时：{addr} 未在 {:?} 内就绪", timeout));
                }
                sleep(Duration::from_millis(80));
            }
            other => return Err(format_probe_error(addr, &other)),
        }
    }
}

fn prepare_agent_port<F>(
    addr: &str,
    probe_timeout: Duration,
    recovery_timeout: Duration,
    mut recover_incompatible_agent: F,
) -> Result<AgentPortState, String>
where
    F: FnMut(&str, &ProbeOutcome) -> Result<bool, String>,
{
    match probe_required_endpoints(addr, probe_timeout) {
        ProbeOutcome::Compatible => Ok(AgentPortState::ReuseExisting),
        ProbeOutcome::Unreachable => Ok(AgentPortState::StartSidecar),
        outcome => {
            let error = format_probe_error(addr, &outcome);
            if !recover_incompatible_agent(addr, &outcome)? {
                return Err(error);
            }
            wait_for_recovered_agent_port(addr, recovery_timeout)
        }
    }
}

fn wait_for_recovered_agent_port(addr: &str, timeout: Duration) -> Result<AgentPortState, String> {
    let deadline = Instant::now() + timeout;
    loop {
        match probe_required_endpoints(addr, AGENT_PROBE_TIMEOUT) {
            ProbeOutcome::Compatible => return Ok(AgentPortState::ReuseExisting),
            ProbeOutcome::Unreachable => return Ok(AgentPortState::StartSidecar),
            outcome => {
                if Instant::now() >= deadline {
                    return Err(format!(
                        "旧 agent 未在 {:?} 内退出，最后一次检查结果：{}",
                        timeout,
                        format_probe_error(addr, &outcome)
                    ));
                }
                sleep(Duration::from_millis(80));
            }
        }
    }
}

fn stop_incompatible_superdev_agent(addr: &str, outcome: &ProbeOutcome) -> Result<bool, String> {
    eprintln!(
        "[SuperDev] found incompatible agent on {addr}: {}",
        format_probe_error(addr, outcome)
    );
    stop_superdev_agent_on_port(addr)
}

#[cfg(unix)]
fn stop_superdev_agent_on_port(addr: &str) -> Result<bool, String> {
    let Some(port) = addr.rsplit(':').next().filter(|value| !value.is_empty()) else {
        return Err(format!("无法解析 agent 端口：{addr}"));
    };
    let output = Command::new("lsof")
        .args(["-nP", "-t", &format!("-iTCP:{port}"), "-sTCP:LISTEN"])
        .output()
        .map_err(|err| format!("查找旧 agent 进程失败: {err}"))?;

    let pids = String::from_utf8_lossy(&output.stdout);
    let mut stopped = false;
    for pid in pids.lines().map(str::trim).filter(|pid| !pid.is_empty()) {
        if !is_superdev_agent_process(pid)? {
            continue;
        }
        eprintln!("[SuperDev] stopping stale agent process {pid} on {addr}");
        let status = Command::new("kill")
            .args(["-TERM", pid])
            .status()
            .map_err(|err| format!("停止旧 agent 进程 {pid} 失败: {err}"))?;
        if !status.success() {
            return Err(format!("停止旧 agent 进程 {pid} 失败：kill 返回 {status}"));
        }
        stopped = true;
    }
    Ok(stopped)
}

#[cfg(not(unix))]
fn stop_superdev_agent_on_port(_addr: &str) -> Result<bool, String> {
    Ok(false)
}

#[cfg(unix)]
fn is_superdev_agent_process(pid: &str) -> Result<bool, String> {
    let output = Command::new("ps")
        .args(["-p", pid, "-o", "comm="])
        .output()
        .map_err(|err| format!("读取旧 agent 进程 {pid} 信息失败: {err}"))?;
    if !output.status.success() {
        return Ok(false);
    }
    let command = String::from_utf8_lossy(&output.stdout);
    let command = command.trim();
    let name = std::path::Path::new(command)
        .file_name()
        .and_then(|value| value.to_str())
        .unwrap_or(command);
    Ok(name == "superdev-agent" || name.starts_with("superdev-agent-"))
}

fn format_probe_error(addr: &str, outcome: &ProbeOutcome) -> String {
    match outcome {
        ProbeOutcome::Compatible => String::new(),
        ProbeOutcome::Unreachable => format!("agent 未监听：{addr}"),
        ProbeOutcome::InvalidResponse { endpoint } => format!(
            "agent 兼容性检查失败：{addr}{endpoint} 返回了无法解析的响应，请确认端口没有被其他进程占用"
        ),
        ProbeOutcome::Incompatible { endpoint, status } => format!(
            "agent 兼容性检查失败：{addr}{endpoint} 返回 {status}，通常是旧版 agent 占用了端口；请退出旧 SuperDev 或停止占用该端口的旧 agent 后重启"
        ),
    }
}

fn probe_required_endpoints(addr: &str, timeout: Duration) -> ProbeOutcome {
    for endpoint in REQUIRED_AGENT_ENDPOINTS {
        match probe_endpoint(addr, endpoint, timeout) {
            EndpointProbe::Status(200) => {}
            EndpointProbe::Status(status) => {
                return ProbeOutcome::Incompatible { endpoint, status }
            }
            EndpointProbe::Unreachable => return ProbeOutcome::Unreachable,
            EndpointProbe::Invalid => return ProbeOutcome::InvalidResponse { endpoint },
        }
    }
    ProbeOutcome::Compatible
}

fn probe_endpoint(addr: &str, endpoint: &'static str, timeout: Duration) -> EndpointProbe {
    let mut stream = match TcpStream::connect(addr) {
        Ok(stream) => stream,
        Err(_) => return EndpointProbe::Unreachable,
    };
    let _ = stream.set_read_timeout(Some(timeout));
    let _ = stream.set_write_timeout(Some(timeout));

    let request = format!("GET {endpoint} HTTP/1.1\r\nHost: {addr}\r\nConnection: close\r\n\r\n");
    if stream.write_all(request.as_bytes()).is_err() {
        return EndpointProbe::Invalid;
    }

    let mut response = Vec::with_capacity(256);
    let mut buf = [0_u8; 128];
    loop {
        let n = match stream.read(&mut buf) {
            Ok(0) => break,
            Ok(n) => n,
            Err(_) => return EndpointProbe::Invalid,
        };
        response.extend_from_slice(&buf[..n]);
        // TCP 可能把状态行和 header 拆成多次读取；只要拿到首行即可解析。
        if response.windows(2).any(|w| w == b"\r\n") || response.len() >= 1024 {
            break;
        }
    }
    if response.is_empty() {
        return EndpointProbe::Invalid;
    }
    let response = String::from_utf8_lossy(&response);
    let Some(status) = response
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        .and_then(|value| value.parse::<u16>().ok())
    else {
        return EndpointProbe::Invalid;
    };
    EndpointProbe::Status(status)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::TcpListener;
    use std::thread;

    fn serve_statuses(statuses: Vec<(&'static str, u16)>) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind fake agent");
        let addr = listener.local_addr().expect("fake agent addr").to_string();
        thread::spawn(move || {
            for (path, status) in statuses {
                let (mut stream, _) = listener.accept().expect("accept probe");
                let mut buf = [0_u8; 512];
                let n = stream.read(&mut buf).expect("read probe");
                let request = String::from_utf8_lossy(&buf[..n]);
                assert!(
                    request.starts_with(&format!("GET {path} ")),
                    "unexpected request: {request}"
                );
                let reason = if status == 200 { "OK" } else { "Not Found" };
                write!(
                    stream,
                    "HTTP/1.1 {status} {reason}\r\nContent-Length: 2\r\n\r\n[]"
                )
                .expect("write response");
                stream.flush().expect("flush response");
            }
        });
        addr
    }

    #[test]
    fn probe_reports_compatible_when_required_remote_endpoints_exist() {
        let addr = serve_statuses(vec![
            ("/api/hosts", 200),
            ("/api/tunnels", 200),
            (
                "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
                200,
            ),
            ("/api/projects/__compat_probe__/ingress", 200),
        ]);

        let outcome = probe_required_endpoints(&addr, Duration::from_secs(1));

        assert_eq!(outcome, ProbeOutcome::Compatible);
    }

    #[test]
    fn probe_reports_incompatible_when_existing_agent_lacks_remote_endpoints() {
        let addr = serve_statuses(vec![("/api/hosts", 404)]);

        let outcome = probe_required_endpoints(&addr, Duration::from_secs(1));

        assert_eq!(
            outcome,
            ProbeOutcome::Incompatible {
                endpoint: "/api/hosts",
                status: 404,
            }
        );
    }

    #[test]
    fn probe_reports_incompatible_when_existing_agent_lacks_template_detail_endpoint() {
        let addr = serve_statuses(vec![
            ("/api/hosts", 200),
            ("/api/tunnels", 200),
            (
                "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
                404,
            ),
        ]);

        let outcome = probe_required_endpoints(&addr, Duration::from_secs(1));

        assert_eq!(
            outcome,
            ProbeOutcome::Incompatible {
                endpoint: "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
                status: 404,
            }
        );
    }

    #[test]
    fn probe_reports_incompatible_when_existing_agent_lacks_project_ingress_endpoint() {
        let addr = serve_statuses(vec![
            ("/api/hosts", 200),
            ("/api/tunnels", 200),
            (
                "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
                200,
            ),
            ("/api/projects/__compat_probe__/ingress", 404),
        ]);

        let outcome = probe_required_endpoints(&addr, Duration::from_secs(1));

        assert_eq!(
            outcome,
            ProbeOutcome::Incompatible {
                endpoint: "/api/projects/__compat_probe__/ingress",
                status: 404,
            }
        );
    }

    #[test]
    fn prepare_agent_port_recovers_incompatible_agent_before_starting_sidecar() {
        let addr = serve_statuses(vec![
            ("/api/hosts", 200),
            ("/api/tunnels", 200),
            (
                "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
                200,
            ),
            ("/api/projects/__compat_probe__/ingress", 404),
        ]);
        let mut recovery_calls = 0;

        let state = prepare_agent_port(
            &addr,
            Duration::from_secs(1),
            Duration::from_secs(1),
            |_, outcome| {
                recovery_calls += 1;
                assert_eq!(
                    outcome,
                    &ProbeOutcome::Incompatible {
                        endpoint: "/api/projects/__compat_probe__/ingress",
                        status: 404,
                    }
                );
                Ok(true)
            },
        )
        .expect("recover stale agent");

        assert_eq!(state, AgentPortState::StartSidecar);
        assert_eq!(recovery_calls, 1);
    }

    #[test]
    fn prepare_agent_port_reports_incompatible_agent_when_recovery_is_not_safe() {
        let addr = serve_statuses(vec![
            ("/api/hosts", 200),
            ("/api/tunnels", 200),
            (
                "/api/pipeline/templates/builtin/go-binary-build?version=1.0.0",
                200,
            ),
            ("/api/projects/__compat_probe__/ingress", 404),
        ]);

        let err = prepare_agent_port(
            &addr,
            Duration::from_secs(1),
            Duration::from_secs(1),
            |_, _| Ok(false),
        )
        .expect_err("unsafe recovery should keep compatibility error");

        assert!(err.contains("/api/projects/__compat_probe__/ingress 返回 404"));
    }
}
