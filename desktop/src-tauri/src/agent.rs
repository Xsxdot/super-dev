use std::fs;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::Mutex;
use std::thread::sleep;
use std::time::{Duration, Instant};

use crate::mcp_install::resolve_sidecar_binary;
use tauri::{AppHandle, Manager};
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

const AGENT_HEALTH_ENDPOINT: &str = "/api/exec/health";
const AGENT_START_TIMEOUT: Duration = Duration::from_secs(5);
const AGENT_PROBE_TIMEOUT: Duration = Duration::from_millis(300);
const AGENT_PORT_RECOVERY_TIMEOUT: Duration = Duration::from_secs(2);
const JS_DEBUG_DIR_NAME: &str = "js-debug";
const JS_DEBUG_SERVER_RELATIVE_PATH: [&str; 2] = ["src", "dapDebugServer.js"];
const JS_DEBUG_VERSION_FILE: &str = ".superdev-version";

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
    StartSidecar,
}

pub struct AgentProcess(pub Mutex<Option<CommandChild>>);

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

    /// start 启动本地 agent sidecar。
    ///
    /// 参数：
    ///   - app: Tauri AppHandle，用于解析并启动 sidecar。
    ///
    /// 返回：
    ///   - Ok 表示当前桌面端自带的 agent 已启动并就绪
    ///   - Err 表示 sidecar 缺失、启动失败，或端口被其他进程占用
    ///
    /// 注意：
    ///   - dev 模式使用 57018，正式构建使用 57017
    ///   - 端口上已有 superdev-agent 时先停止旧进程，避免复用后退出时无法清理
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
            stop_existing_superdev_agent,
        )? {
            AgentPortState::StartSidecar => {}
        }

        let data_dir_path = PathBuf::from(&data_dir);
        if let Some(resource_dir) = resolve_resource_dir(app) {
            if let Err(err) = sync_js_debug_resource(&resource_dir, &data_dir_path) {
                eprintln!("[SuperDev] 同步 js-debug 资源失败: {err}");
            }
        }

        let mut args = vec![
            "--addr".to_string(),
            addr.to_string(),
            "--data".to_string(),
            data_dir,
        ];
        if let Ok(sample) = resolve_sidecar_binary(app, "superdev-sample") {
            args.push("--sample-binary".to_string());
            args.push(sample.to_string_lossy().to_string());
        }
        if let Some(install_dir) = resolve_agent_install_dir(app) {
            args.push("--install-binaries".to_string());
            args.push(install_dir.to_string_lossy().to_string());
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
    /// 注意：start 不复用端口上的旧 agent，因此退出时只清理自己启动的 child。
    pub fn stop(&self) {
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(child) = guard.take() {
            let _ = child.kill();
            println!("[SuperDev] agent stopped");
        }
    }
}

fn js_debug_server_path(root: &Path) -> PathBuf {
    root.join(JS_DEBUG_SERVER_RELATIVE_PATH[0])
        .join(JS_DEBUG_SERVER_RELATIVE_PATH[1])
}

fn resolve_resource_dir(app: &AppHandle) -> Option<PathBuf> {
    if let Ok(resource_dir) = app.path().resource_dir() {
        return Some(resource_dir);
    }
    if cfg!(debug_assertions) {
        return std::env::current_dir()
            .ok()
            .map(|dir| dir.join("src-tauri").join("resources"));
    }
    None
}

fn resolve_agent_install_dir(app: &AppHandle) -> Option<PathBuf> {
    if let Ok(resource_dir) = app.path().resource_dir() {
        let candidate = resource_dir.join("agent-install");
        if candidate.is_dir() {
            return Some(candidate);
        }
    }
    if cfg!(debug_assertions) {
        let candidate = std::env::current_dir()
            .ok()?
            .join("src-tauri")
            .join("resources")
            .join("agent-install");
        if candidate.is_dir() {
            return Some(candidate);
        }
    }
    None
}

fn sync_js_debug_resource(resource_dir: &Path, data_dir: &Path) -> Result<(), String> {
    let source = resource_dir.join(JS_DEBUG_DIR_NAME);
    if !js_debug_server_path(&source).is_file() {
        return Ok(());
    }
    let target = data_dir.join(JS_DEBUG_DIR_NAME);
    if js_debug_server_path(&target).is_file() && js_debug_versions_match(&source, &target)? {
        return Ok(());
    }

    fs::create_dir_all(data_dir).map_err(|err| format!("创建 agent data dir 失败: {err}"))?;
    let tmp = data_dir.join("js-debug.tmp");
    remove_path_if_exists(&tmp)?;
    copy_dir_recursive(&source, &tmp)?;
    remove_path_if_exists(&target)?;
    fs::rename(&tmp, &target).map_err(|err| format!("替换 js-debug 资源失败: {err}"))?;
    Ok(())
}

fn js_debug_versions_match(source: &Path, target: &Path) -> Result<bool, String> {
    let source_version = source.join(JS_DEBUG_VERSION_FILE);
    let target_version = target.join(JS_DEBUG_VERSION_FILE);
    if !source_version.is_file() || !target_version.is_file() {
        return Ok(false);
    }
    let source = fs::read_to_string(&source_version)
        .map_err(|err| format!("读取 js-debug 资源版本失败: {err}"))?;
    let target = fs::read_to_string(&target_version)
        .map_err(|err| format!("读取已安装 js-debug 版本失败: {err}"))?;
    Ok(source == target)
}

fn remove_path_if_exists(path: &Path) -> Result<(), String> {
    match fs::symlink_metadata(path) {
        Ok(meta) if meta.is_dir() => {
            fs::remove_dir_all(path).map_err(|err| format!("删除旧 js-debug 目录失败: {err}"))
        }
        Ok(_) => fs::remove_file(path).map_err(|err| format!("删除旧 js-debug 文件失败: {err}")),
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(err) => Err(format!("检查 js-debug 路径失败: {err}")),
    }
}

fn copy_dir_recursive(source: &Path, target: &Path) -> Result<(), String> {
    fs::create_dir_all(target).map_err(|err| format!("创建 js-debug 目录失败: {err}"))?;
    for entry in fs::read_dir(source).map_err(|err| format!("读取 js-debug 资源目录失败: {err}"))?
    {
        let entry = entry.map_err(|err| format!("读取 js-debug 资源项失败: {err}"))?;
        let source_path = entry.path();
        let target_path = target.join(entry.file_name());
        let file_type = entry
            .file_type()
            .map_err(|err| format!("读取 js-debug 资源项类型失败: {err}"))?;
        if file_type.is_dir() {
            copy_dir_recursive(&source_path, &target_path)?;
        } else if file_type.is_file() {
            fs::copy(&source_path, &target_path)
                .map_err(|err| format!("复制 js-debug 资源文件失败: {err}"))?;
        }
    }
    Ok(())
}

fn wait_for_compatible_agent(addr: &str, timeout: Duration) -> Result<(), String> {
    let deadline = Instant::now() + timeout;
    loop {
        match probe_agent_health(addr, AGENT_PROBE_TIMEOUT) {
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
    mut stop_existing_agent: F,
) -> Result<AgentPortState, String>
where
    F: FnMut(&str, &ProbeOutcome) -> Result<bool, String>,
{
    match probe_agent_health(addr, probe_timeout) {
        ProbeOutcome::Unreachable => Ok(AgentPortState::StartSidecar),
        outcome => {
            let error = format_existing_agent_error(addr, &outcome);
            if !stop_existing_agent(addr, &outcome)? {
                return Err(error);
            }
            wait_for_released_agent_port(addr, recovery_timeout)
        }
    }
}

fn wait_for_released_agent_port(addr: &str, timeout: Duration) -> Result<AgentPortState, String> {
    let deadline = Instant::now() + timeout;
    loop {
        match probe_agent_health(addr, AGENT_PROBE_TIMEOUT) {
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

fn stop_existing_superdev_agent(addr: &str, outcome: &ProbeOutcome) -> Result<bool, String> {
    if outcome == &ProbeOutcome::Compatible {
        eprintln!("[SuperDev] found existing agent on {addr}; taking sidecar ownership");
    } else {
        eprintln!(
            "[SuperDev] found incompatible agent on {addr}: {}",
            format_probe_error(addr, outcome)
        );
    }
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
        ProbeOutcome::Compatible => format!("agent 已监听且接口兼容：{addr}"),
        ProbeOutcome::Unreachable => format!("agent 未监听：{addr}"),
        ProbeOutcome::InvalidResponse { endpoint } => format!(
            "agent 兼容性检查失败：{addr}{endpoint} 返回了无法解析的响应，请确认端口没有被其他进程占用"
        ),
        ProbeOutcome::Incompatible { endpoint, status } => format!(
            "agent 兼容性检查失败：{addr}{endpoint} 返回 {status}，通常是旧版 agent 占用了端口；请退出旧 SuperDev 或停止占用该端口的旧 agent 后重启"
        ),
    }
}

fn format_existing_agent_error(addr: &str, outcome: &ProbeOutcome) -> String {
    if outcome == &ProbeOutcome::Compatible {
        return format!(
            "agent 端口已被已有进程占用：{addr}；SuperDev 需要启动当前桌面端自带的 agent，请退出占用该端口的旧 SuperDev 或 agent 后重启"
        );
    }
    format_probe_error(addr, outcome)
}

fn probe_agent_health(addr: &str, timeout: Duration) -> ProbeOutcome {
    match probe_endpoint(addr, AGENT_HEALTH_ENDPOINT, timeout) {
        EndpointProbe::Status(200) => ProbeOutcome::Compatible,
        EndpointProbe::Status(status) => ProbeOutcome::Incompatible {
            endpoint: AGENT_HEALTH_ENDPOINT,
            status,
        },
        EndpointProbe::Unreachable => ProbeOutcome::Unreachable,
        EndpointProbe::Invalid => ProbeOutcome::InvalidResponse {
            endpoint: AGENT_HEALTH_ENDPOINT,
        },
    }
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
    use std::fs;
    use std::net::TcpListener;
    use std::path::PathBuf;
    use std::thread;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn temp_test_dir(name: &str) -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("clock")
            .as_nanos();
        let dir = std::env::temp_dir().join(format!(
            "superdev-agent-{name}-{}-{unique}",
            std::process::id()
        ));
        fs::create_dir_all(&dir).expect("mkdir temp test dir");
        dir
    }

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
    fn probe_reports_compatible_when_agent_health_exists() {
        let addr = serve_statuses(vec![("/api/exec/health", 200)]);

        let outcome = probe_agent_health(&addr, Duration::from_secs(1));

        assert_eq!(outcome, ProbeOutcome::Compatible);
    }

    #[test]
    fn sync_js_debug_resource_copies_standalone_server_into_data_dir() {
        let root = temp_test_dir("js-debug-sync");
        let resource_dir = root.join("resources");
        let data_dir = root.join("data");
        let source_server = resource_dir
            .join("js-debug")
            .join("src")
            .join("dapDebugServer.js");
        fs::create_dir_all(source_server.parent().expect("source parent")).expect("mkdir source");
        fs::write(&source_server, "console.log('js-debug');").expect("write source");

        sync_js_debug_resource(&resource_dir, &data_dir).expect("sync js-debug");

        let target_server = data_dir
            .join("js-debug")
            .join("src")
            .join("dapDebugServer.js");
        assert_eq!(
            fs::read_to_string(target_server).expect("read target"),
            "console.log('js-debug');"
        );

        fs::remove_dir_all(root).expect("cleanup temp test dir");
    }

    #[test]
    fn probe_reports_incompatible_when_agent_health_is_missing() {
        let addr = serve_statuses(vec![("/api/exec/health", 404)]);

        let outcome = probe_agent_health(&addr, Duration::from_secs(1));

        assert_eq!(
            outcome,
            ProbeOutcome::Incompatible {
                endpoint: "/api/exec/health",
                status: 404,
            }
        );
    }

    #[test]
    fn prepare_agent_port_recovers_incompatible_agent_before_starting_sidecar() {
        let addr = serve_statuses(vec![("/api/exec/health", 404)]);
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
                        endpoint: "/api/exec/health",
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
    fn prepare_agent_port_stops_existing_compatible_agent_before_starting_sidecar() {
        let addr = serve_statuses(vec![("/api/exec/health", 200)]);
        let mut recovery_calls = 0;

        let state = prepare_agent_port(
            &addr,
            Duration::from_secs(1),
            Duration::from_secs(1),
            |_, outcome| {
                recovery_calls += 1;
                assert_eq!(outcome, &ProbeOutcome::Compatible);
                Ok(true)
            },
        )
        .expect("recover stale compatible agent");

        assert_eq!(state, AgentPortState::StartSidecar);
        assert_eq!(recovery_calls, 1);
    }

    #[test]
    fn prepare_agent_port_reports_incompatible_agent_when_recovery_is_not_safe() {
        let addr = serve_statuses(vec![("/api/exec/health", 404)]);

        let err = prepare_agent_port(
            &addr,
            Duration::from_secs(1),
            Duration::from_secs(1),
            |_, _| Ok(false),
        )
        .expect_err("unsafe recovery should keep compatibility error");

        assert!(err.contains("/api/exec/health 返回 404"));
    }
}
