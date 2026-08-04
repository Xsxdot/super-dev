use std::fs;
use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::Mutex;
use std::thread::sleep;
use std::time::{Duration, Instant};

use crate::mcp_install::{resolve_sidecar_binary, resolve_user_home_dir};
use tauri::{AppHandle, Manager};
use tauri_plugin_shell::process::{CommandChild, CommandEvent};
use tauri_plugin_shell::ShellExt;

/// 探测端点：必须用 bypass 白名单里的 security health——
/// 鉴权常开后 /api/exec/health 受保护，探测会永远 401 并误判「不兼容」。
const AGENT_HEALTH_ENDPOINT: &str = "/api/security/health";
const AGENT_START_TIMEOUT: Duration = Duration::from_secs(5);
const AGENT_PROBE_TIMEOUT: Duration = Duration::from_millis(300);
const AGENT_PORT_RECOVERY_TIMEOUT: Duration = Duration::from_secs(2);
/// 退出时发出 SIGTERM 后，等待 agent 优雅退出的最长秒数；超时则 SIGKILL 兜底。
const AGENT_TERM_GRACE: u32 = 3;
/// 优雅退出轮询的间隔。
const AGENT_TERM_POLL: Duration = Duration::from_millis(100);
const JS_DEBUG_DIR_NAME: &str = "js-debug";
const JS_DEBUG_SERVER_RELATIVE_PATH: [&str; 2] = ["src", "dapDebugServer.js"];
const JS_DEBUG_VERSION_FILE: &str = ".superdev-version";
const AGENT_SIDECAR_LOG_FILE: &str = "agent-sidecar.log";
const AGENT_SIDECAR_LOG_ROTATED_FILE: &str = "agent-sidecar.log.1";
const AGENT_SIDECAR_LOG_MAX_BYTES: u64 = 4 * 1024 * 1024;

#[derive(Debug, PartialEq, Eq)]
enum ProbeOutcome {
    /// 200：接口兼容。version 来自响应体 JSON 的 "version" 字段，解析失败为 None——
    /// 版本只用于 UI 呈现/升级提示，不是兼容性判定依据。
    Compatible {
        version: Option<String>,
    },
    Unreachable,
    InvalidResponse {
        endpoint: &'static str,
    },
    Incompatible {
        endpoint: &'static str,
        status: u16,
    },
}

/// EndpointProbe 是 probe_endpoint 的底层结果：连通性 + 原始状态码/响应体，
/// 由上层（probe_agent_health / fetch_local_token_path）按各自需要解读 body。
enum EndpointProbe {
    Response { status: u16, body: String },
    Unreachable,
    Invalid,
}

#[derive(Debug, PartialEq, Eq)]
enum AgentPortState {
    StartSidecar,
    /// 端口上已有兼容的 agent：直接复用，不启动 sidecar（见 prepare_agent_port）。
    AttachExisting {
        version: Option<String>,
    },
}

/// 桌面端与本机 agent 的连接形态。
#[derive(Clone, Debug, PartialEq)]
pub enum AgentMode {
    /// 桌面端自己拉起的 sidecar 子进程（退出时须优雅停掉，孤儿修复语义保留）。
    Sidecar,
    /// 复用本机既有 agent（服务化/headless 安装）：桌面端只是客户端，退出时不动它。
    Attached { version: Option<String> },
}

/// TerminateOutcome 记录一次优雅终止的最终结果，用于日志与测试断言。
#[derive(Debug, PartialEq, Eq)]
enum TerminateOutcome {
    /// SIGTERM 后进程在超时内退出，未触发强杀。
    ExitedAfterTerm,
    /// 进程未在超时内退出，已 SIGKILL 兜底。
    ForceKilled,
}

/// AgentProcess 持有桌面端与本机 agent 的连接状态。
///
/// 字段：
///   - child: 仅 Sidecar 模式下为 Some——桌面端自己拉起并需要负责终止的子进程句柄。
///   - mode: 当前连接形态（见 AgentMode），stop() 据此决定是否终止 child。
pub struct AgentProcess {
    pub child: Mutex<Option<CommandChild>>,
    pub mode: Mutex<Option<AgentMode>>,
}

fn open_agent_sidecar_log(data_dir: &Path, max_bytes: u64) -> Result<fs::File, String> {
    fs::create_dir_all(data_dir)
        .map_err(|err| format!("创建 agent 日志目录 {} 失败: {err}", data_dir.display()))?;
    let current = data_dir.join(AGENT_SIDECAR_LOG_FILE);
    let rotated = data_dir.join(AGENT_SIDECAR_LOG_ROTATED_FILE);
    let should_rotate = match fs::metadata(&current) {
        Ok(metadata) => metadata.len() >= max_bytes,
        Err(err) if err.kind() == std::io::ErrorKind::NotFound => false,
        Err(err) => {
            return Err(format!(
                "读取 agent 日志元数据 {} 失败: {err}",
                current.display()
            ));
        }
    };
    // 只保留一代启动日志，既留下上次首启失败证据，也避免桌面常驻后无限占用用户目录。
    if should_rotate {
        if let Err(err) = fs::remove_file(&rotated) {
            if err.kind() != std::io::ErrorKind::NotFound {
                return Err(format!(
                    "删除旧 agent 轮转日志 {} 失败: {err}",
                    rotated.display()
                ));
            }
        }
        fs::rename(&current, &rotated).map_err(|err| {
            format!(
                "轮转 agent 日志 {} -> {} 失败: {err}",
                current.display(),
                rotated.display()
            )
        })?;
    }
    fs::OpenOptions::new()
        .create(true)
        .append(true)
        .open(&current)
        .map_err(|err| format!("打开 agent 日志 {} 失败: {err}", current.display()))
}

fn write_agent_sidecar_log(file: &mut fs::File, stream: &str, message: &str) -> Result<(), String> {
    let message = message.trim_end_matches(|ch| ch == '\r' || ch == '\n');
    writeln!(file, "[{stream}] {message}")
        .map_err(|err| format!("写入 agent sidecar 日志失败: {err}"))?;
    // 首启可能在下一条事件前退出；逐条刷新确保关键错误已经落盘。
    file.flush()
        .map_err(|err| format!("刷新 agent sidecar 日志失败: {err}"))
}

impl AgentProcess {
    /// new 创建 AgentProcess 容器。
    ///
    /// 参数：无。
    ///
    /// 返回：持有可选 sidecar 子进程句柄与连接模式的状态对象（初始均为 None）。
    ///
    /// 注意：实际 agent 启动/attach 决策只在 start 中发生。
    pub fn new() -> Self {
        AgentProcess {
            child: Mutex::new(None),
            mode: Mutex::new(None),
        }
    }

    /// start 启动本地 agent，或复用端口上已兼容的既有 agent。
    ///
    /// 参数：
    ///   - app: Tauri AppHandle，用于解析并启动 sidecar。
    ///
    /// 返回：
    ///   - Ok 表示当前桌面端已就绪可用的 agent（自带 sidecar 或复用既有 agent）
    ///   - Err 表示 sidecar 缺失、启动失败，或端口被不兼容进程占用且恢复不安全
    ///
    /// 注意：
    ///   - dev 模式使用 57018，正式构建使用 57017
    ///   - 端口上已有兼容 agent 时直接 attach 复用，绝不停止它（可能是服务化/headless
    ///     安装，有自己的生命周期）；仅端口上是不兼容/坏响应的占用者时才按原逻辑尝试恢复
    pub fn start(&self, app: &AppHandle) -> Result<(), String> {
        let mut guard = self.child.lock().unwrap_or_else(|e| e.into_inner());
        if guard.is_some() {
            return Ok(());
        }

        let (addr, data_dir_path) = agent_addr_and_data_dir()?;
        let addr = addr.as_str();
        tracing::info!(
            address = addr,
            data_dir = %data_dir_path.display(),
            "agent launch target resolved"
        );

        match prepare_agent_port(
            addr,
            AGENT_PROBE_TIMEOUT,
            AGENT_PORT_RECOVERY_TIMEOUT,
            stop_existing_superdev_agent,
        )? {
            AgentPortState::StartSidecar => {}
            // 兼容 agent 已在端口上运行：attach 复用，不 spawn sidecar，也不碰它的生命周期。
            AgentPortState::AttachExisting { version } => {
                eprintln!(
                    "[SuperDev] 复用本机既有 agent（v{}），不启动 sidecar",
                    version.clone().unwrap_or_else(|| "unknown".into())
                );
                *self.mode.lock().unwrap_or_else(|e| e.into_inner()) =
                    Some(AgentMode::Attached { version });
                return Ok(());
            }
        }

        if let Some(resource_dir) = resolve_resource_dir(app) {
            if let Err(err) = sync_js_debug_resource(&resource_dir, &data_dir_path) {
                eprintln!("[SuperDev] 同步 js-debug 资源失败: {err}");
            }
        }

        let mut args = vec![
            "--addr".to_string(),
            addr.to_string(),
            "--data".to_string(),
            data_dir_path.to_string_lossy().to_string(),
        ];
        if let Ok(sample) = resolve_sidecar_binary(app, "superdev-sample") {
            args.push("--sample-binary".to_string());
            args.push(sample.to_string_lossy().to_string());
        }
        if let Some(install_dir) = resolve_agent_install_dir(app) {
            args.push("--install-binaries".to_string());
            args.push(install_dir.to_string_lossy().to_string());
        }

        let mut sidecar_log =
            match open_agent_sidecar_log(&data_dir_path, AGENT_SIDECAR_LOG_MAX_BYTES) {
                Ok(file) => Some(file),
                Err(err) => {
                    tracing::error!(error = %err, "agent sidecar persistent log unavailable");
                    None
                }
            };
        let (mut rx, child) = app
            .shell()
            .sidecar("superdev-agent")
            .map_err(|e| format!("找不到 agent sidecar: {e}"))?
            .args(args)
            .spawn()
            .map_err(|e| format!("启动 agent 失败: {e}"))?;

        // sidecar 使用有界事件通道；持续消费既避免 stdout/stderr 管道反压卡住 agent，也把首启错误留在用户数据目录。
        tauri::async_runtime::spawn(async move {
            while let Some(event) = rx.recv().await {
                let (stream, message) = match event {
                    CommandEvent::Stdout(bytes) => {
                        let message = String::from_utf8_lossy(&bytes).into_owned();
                        tracing::info!(target: "superdev_agent", line = %message, "agent stdout");
                        ("stdout", message)
                    }
                    CommandEvent::Stderr(bytes) => {
                        let message = String::from_utf8_lossy(&bytes).into_owned();
                        tracing::warn!(target: "superdev_agent", line = %message, "agent stderr");
                        ("stderr", message)
                    }
                    CommandEvent::Error(error) => {
                        tracing::error!(target: "superdev_agent", error = %error, "agent event stream failed");
                        ("error", error)
                    }
                    CommandEvent::Terminated(payload) => {
                        let message =
                            format!("code={:?} signal={:?}", payload.code, payload.signal);
                        tracing::info!(target: "superdev_agent", %message, "agent terminated");
                        ("terminated", message)
                    }
                    _ => continue,
                };
                if let Some(file) = sidecar_log.as_mut() {
                    if let Err(err) = write_agent_sidecar_log(file, stream, &message) {
                        tracing::error!(error = %err, "agent sidecar persistent log disabled after write failure");
                        sidecar_log = None;
                    }
                }
            }
        });

        if let Err(err) = wait_for_compatible_agent(addr, AGENT_START_TIMEOUT) {
            let _ = child.kill();
            return Err(err);
        }

        println!("[SuperDev] agent started");
        *self.mode.lock().unwrap_or_else(|e| e.into_inner()) = Some(AgentMode::Sidecar);
        *guard = Some(child);
        Ok(())
    }

    /// stop 停止当前 Tauri 实例启动的 agent sidecar。
    ///
    /// 参数：无。
    ///
    /// 返回：无。
    ///
    /// 注意：
    ///   - 仅 Sidecar 模式会真正终止子进程；Attached 模式的 agent 不是我们的子进程，
    ///     它有自己的生命周期（launchd/systemd），退出时必须保持运行，见 stop_with_mode_gate。
    ///   - Sidecar 终止先发 SIGTERM，给它时间停掉所有托管服务（避免孤儿进程），
    ///     超时未退出再 SIGKILL 兜底。
    ///   - 非 Unix 平台无 SIGTERM 语义，直接走 child.kill()。
    pub fn stop(&self) {
        let mode = self.mode.lock().unwrap_or_else(|e| e.into_inner()).clone();
        stop_with_mode_gate(mode.as_ref(), || {
            let mut guard = self.child.lock().unwrap_or_else(|e| e.into_inner());
            if let Some(child) = guard.take() {
                #[cfg(unix)]
                {
                    let pid = child.pid();
                    println!("[SuperDev] stopping agent gracefully pid={pid}");
                    let outcome = terminate_pid_gracefully(
                        pid,
                        AGENT_TERM_GRACE,
                        || send_sigterm(pid),
                        || pid_alive(pid),
                        || force_sigkill(pid),
                    );
                    let _ = outcome;
                    // 兜底强杀直接对 pid 发 SIGKILL，而不是调用 child.kill()，避免闭包消费 child 所有权。
                    drop(child);
                }
                #[cfg(not(unix))]
                {
                    let _ = child.kill();
                }
                println!("[SuperDev] agent stopped");
            }
        });
    }
}

/// stop_with_mode_gate 是 stop() 的核心决策逻辑：只有 Sidecar 模式才终止子进程。
///
/// 参数：
///   - mode: 当前记录的连接形态（None 视为未记录/尚未启动，同样不终止）。
///   - terminate: 终止子进程的实际动作，抽成闭包便于单测注入计数断言。
///
/// 注意：Attached 模式的 agent 不归桌面端管理，退出时保持运行——绝不能对它发信号，
/// 否则会杀掉一个可能承载其他客户端连接的服务化/headless agent。
fn stop_with_mode_gate<T: FnMut()>(mode: Option<&AgentMode>, mut terminate: T) {
    match mode {
        Some(AgentMode::Sidecar) => terminate(),
        Some(AgentMode::Attached { .. }) => {
            eprintln!("[SuperDev] attached agent 不归桌面端管理，退出时保持运行");
        }
        // None = agent 从未成功启动（sidecar 缺失/启动失败），没有进程需要停止；
        // 与 Attached 分开打日志，避免误导排障（此时并不存在 attached agent）。
        None => eprintln!("[SuperDev] 无已记录的 agent 进程，退出时无需停止"),
    }
}

/// agent_addr_and_data_dir 解析桌面端自带 sidecar 使用的监听地址与数据目录。
///
/// 返回：
///   - addr: 形如 "127.0.0.1:57017" 的本机回环地址。
///   - data_dir: 桌面端自带 sidecar 的用户数据目录。
///
/// 注意：
///   - dev 构建（`tauri dev`）与正式构建各用独立端口/目录，避免同时运行时冲突。
///   - 仅对 Sidecar/尚未 attach 的场景有意义；Attached 模式下真实数据目录未必是这里
///     算出来的值（headless 安装可能自定义 --data），local_agent_token 会改问 agent 本身。
fn agent_addr_and_data_dir() -> Result<(String, PathBuf), String> {
    let (addr, data_dir_name) = if cfg!(debug_assertions) {
        ("127.0.0.1:57018", ".superdev-dev")
    } else {
        ("127.0.0.1:57017", ".superdev")
    };
    let data_dir = resolve_user_home_dir()?.join(data_dir_name);
    Ok((addr.to_string(), data_dir))
}

/// local_agent_base_url 返回本机 agent 的 HTTP origin（如 `http://127.0.0.1:57017`）。
///
/// 返回：
///   - Ok: 可直接拼接 `/api/...` 的 origin。
///   - Err: 用户目录解析失败时的说明。
///
/// 注意：
///   - 明文 HTTP 是刻意的：桌面端与 agent 只走本机回环，agent 侧对 loopback 明文
///     连接有单端口首字节嗅探豁免，这里再套一层 TLS 反而会连不上。
///   - 远端接入（`mcp_install::remote_install`）用它作为 integrations 代理的基址：
///     桌面端自始至终只跟**本机** agent 说话，目标机凭据由本机 agent 注入。
pub(crate) fn local_agent_base_url() -> Result<String, String> {
    let (addr, _) = agent_addr_and_data_dir()?;
    Ok(format!("http://{addr}"))
}

/// 前端底栏呈现用的桌面端-agent 连接形态快照。
#[derive(serde::Serialize, Clone)]
pub struct AgentConnectionInfo {
    pub mode: String, // "sidecar" | "attached" | "unknown"
    pub version: Option<String>,
    pub addr: String,
}

/// Tauri 命令：返回当前与本机 agent 的连接形态（前端底栏呈现用）。
///
/// 注意：addr 解析用户目录失败时（极端场景）退化为空字符串，不让这个只读信息
/// 查询命令因为目录解析失败而报错阻断前端渲染。
#[tauri::command]
pub fn agent_connection_info(state: tauri::State<'_, AgentProcess>) -> AgentConnectionInfo {
    let addr = agent_addr_and_data_dir()
        .map(|(addr, _)| addr)
        .unwrap_or_default();
    match state
        .mode
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .as_ref()
    {
        Some(AgentMode::Sidecar) => AgentConnectionInfo {
            mode: "sidecar".to_string(),
            version: None,
            addr,
        },
        Some(AgentMode::Attached { version }) => AgentConnectionInfo {
            mode: "attached".to_string(),
            version: version.clone(),
            addr,
        },
        None => AgentConnectionInfo {
            mode: "unknown".to_string(),
            version: None,
            addr,
        },
    }
}

/// Tauri 命令：读取本机 agent 的 local-access-token 交给 webview 作 Authorization 头。
///
/// 参数：无（连接模式从 AgentProcess 状态读取）。
///
/// 返回：
///   - Ok: token 明文（已 trim）。
///   - Err: 面向用户的中文指引（不含 token 值，只含路径与排查建议）。
///
/// 注意：sidecar 模式直接读桌面端已知数据目录；attach 模式经 /api/security/health
/// 发现路径——因为 attach 的既有 agent 可能是 headless 安装且自定义了 --data，
/// 桌面端算出来的默认数据目录未必是它真实使用的目录，必须以 agent 自己披露的为准。
#[tauri::command]
pub fn local_agent_token(state: tauri::State<'_, AgentProcess>) -> Result<String, String> {
    let (addr, data_dir) = agent_addr_and_data_dir()?;
    let path = match state
        .mode
        .lock()
        .unwrap_or_else(|e| e.into_inner())
        .as_ref()
    {
        Some(AgentMode::Attached { .. }) => {
            // attach：数据目录未必是桌面端默认值（headless 安装可自定义 --data），
            // 以 agent 自己披露的路径为准。
            fetch_local_token_path(&addr)
                .map_err(|e| format!("向本机 agent 查询凭据路径失败: {e}"))?
        }
        _ => data_dir
            .join(crate::LOCAL_TOKEN_FILE_NAME)
            .to_string_lossy()
            .into_owned(),
    };
    std::fs::read_to_string(&path)
        .map(|s| s.trim().to_string())
        .map_err(|e| {
            eprintln!("[SuperDev] 读取本机 agent 凭据失败 path={path}: {e}");
            format!(
                "读取本机 agent 凭据失败（{path}）。若 agent 为系统级安装（属主非当前用户），请以相同用户运行桌面端或改用远程接入: {e}"
            )
        })
}

/// terminate_pid_gracefully 以「先 SIGTERM、轮询、超时再 SIGKILL」的策略终止进程。
///
/// 参数：
///   - pid: 目标进程 pid（仅用于日志，实际发信号由闭包封装）。
///   - grace_secs: 等待优雅退出的最长秒数。
///   - send_term: 发送 SIGTERM 的动作（注入便于测试）。
///   - is_alive: 探测进程是否仍存活（注入便于测试）。
///   - force_kill: 超时兜底的强杀动作（注入便于测试）。
///
/// 返回：终止结果（及时退出 / 超时强杀）。
///
/// 注意：把策略与真实系统调用解耦，使「轮询+超时」逻辑可在不发真实信号的前提下单测。
fn terminate_pid_gracefully<T, A, K>(
    pid: u32,
    grace_secs: u32,
    send_term: T,
    is_alive: A,
    mut force_kill: K,
) -> TerminateOutcome
where
    T: FnOnce(),
    A: Fn() -> bool,
    K: FnMut(),
{
    send_term();
    let deadline = Instant::now() + Duration::from_secs(grace_secs as u64);
    loop {
        if !is_alive() {
            println!("[SuperDev] agent exited gracefully after SIGTERM pid={pid}");
            return TerminateOutcome::ExitedAfterTerm;
        }
        if Instant::now() >= deadline {
            eprintln!(
                "[SuperDev] agent did not exit within {grace_secs}s, sending SIGKILL pid={pid}"
            );
            force_kill();
            return TerminateOutcome::ForceKilled;
        }
        sleep(AGENT_TERM_POLL);
    }
}

/// send_sigterm 向指定 pid 发送 SIGTERM（仅 Unix）。
///
/// 注意：失败仅记录日志；进程可能已自行退出，调用方随后会探活并按需兜底强杀。
#[cfg(unix)]
fn send_sigterm(pid: u32) {
    use nix::sys::signal::{kill, Signal};
    use nix::unistd::Pid;
    if let Err(err) = kill(Pid::from_raw(pid as i32), Signal::SIGTERM) {
        eprintln!("[SuperDev] send SIGTERM to agent pid={pid} failed: {err}");
    }
}

/// force_sigkill 向指定 pid 发送 SIGKILL 兜底强杀（仅 Unix）。
#[cfg(unix)]
fn force_sigkill(pid: u32) {
    use nix::sys::signal::{kill, Signal};
    use nix::unistd::Pid;
    if let Err(err) = kill(Pid::from_raw(pid as i32), Signal::SIGKILL) {
        eprintln!("[SuperDev] send SIGKILL to agent pid={pid} failed: {err}");
    }
}

/// pid_alive 探测指定 pid 是否仍存活（仅 Unix，发 0 号信号探测）。
#[cfg(unix)]
fn pid_alive(pid: u32) -> bool {
    use nix::sys::signal::kill;
    use nix::unistd::Pid;
    // kill(pid, None) 不发信号只做存在性/权限检查：Ok=存在，EPERM 也视为存在。
    match kill(Pid::from_raw(pid as i32), None) {
        Ok(_) => true,
        Err(nix::errno::Errno::EPERM) => true,
        Err(_) => false,
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
            ProbeOutcome::Compatible { .. } => return Ok(()),
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
        // 兼容 agent 在跑：attach 复用，绝不杀——它可能是服务化/headless 安装，
        // 有自己的生命周期（launchd/systemd），桌面端只是它的一个客户端。
        ProbeOutcome::Compatible { version } => Ok(AgentPortState::AttachExisting { version }),
        // 不兼容/坏响应：保留既有恢复路径（仅对进程名为 superdev-agent* 的占用者 SIGTERM）
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
    // 走到这里时 outcome 只可能是 Incompatible/InvalidResponse：Compatible 已经在
    // prepare_agent_port 里被 attach 分支接住，不会再调用本函数。分支保留是为了
    // 防御性兼容未来其他调用方直接传入 Compatible。
    if matches!(outcome, ProbeOutcome::Compatible { .. }) {
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
        ProbeOutcome::Compatible { .. } => format!("agent 已监听且接口兼容：{addr}"),
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
    if matches!(outcome, ProbeOutcome::Compatible { .. }) {
        return format!(
            "agent 端口已被已有进程占用：{addr}；SuperDev 需要启动当前桌面端自带的 agent，请退出占用该端口的旧 SuperDev 或 agent 后重启"
        );
    }
    format_probe_error(addr, outcome)
}

fn probe_agent_health(addr: &str, timeout: Duration) -> ProbeOutcome {
    match probe_endpoint(addr, AGENT_HEALTH_ENDPOINT, timeout) {
        EndpointProbe::Response { status: 200, body } => ProbeOutcome::Compatible {
            version: parse_health_version(&body),
        },
        EndpointProbe::Response { status, .. } => ProbeOutcome::Incompatible {
            endpoint: AGENT_HEALTH_ENDPOINT,
            status,
        },
        EndpointProbe::Unreachable => ProbeOutcome::Unreachable,
        EndpointProbe::Invalid => ProbeOutcome::InvalidResponse {
            endpoint: AGENT_HEALTH_ENDPOINT,
        },
    }
}

/// 从 security health 的 JSON body 解析 version；解析失败返回 None（不阻断 attach，
/// 版本只用于 UI 呈现与升级提示，不是兼容性门槛）。
fn parse_health_version(body: &str) -> Option<String> {
    serde_json::from_str::<serde_json::Value>(body)
        .ok()?
        .get("version")?
        .as_str()
        .map(|s| s.to_string())
}

/// 从 security health 的 JSON body 解析 local_token_path；用于 attach 模式下
/// local_agent_token 定位既有 agent 实际使用的数据目录（可能是自定义 --data）。
fn parse_local_token_path(body: &str) -> Option<String> {
    serde_json::from_str::<serde_json::Value>(body)
        .ok()?
        .get("local_token_path")?
        .as_str()
        .map(|s| s.to_string())
}

/// fetch_local_token_path 向本机 agent 的 security health 端点查询它自己披露的
/// local-access-token 文件路径。
///
/// 注意：仅 loopback 请求才会拿到 local_token_path（agent 侧的隐私边界，见
/// agent/api/security_handler.go 的 isLoopbackRequest）；桌面端与 agent 只通过
/// 127.0.0.1 通信，天然满足这个条件。
fn fetch_local_token_path(addr: &str) -> Result<String, String> {
    match probe_endpoint(addr, AGENT_HEALTH_ENDPOINT, AGENT_PROBE_TIMEOUT) {
        EndpointProbe::Response { status: 200, body } => {
            parse_local_token_path(&body).ok_or_else(|| {
                format!("agent 响应缺少 local_token_path 字段：{addr}{AGENT_HEALTH_ENDPOINT}")
            })
        }
        EndpointProbe::Response { status, .. } => Err(format!(
            "agent 健康检查返回非预期状态 {status}：{addr}{AGENT_HEALTH_ENDPOINT}"
        )),
        EndpointProbe::Unreachable => Err(format!("无法连接本机 agent：{addr}")),
        EndpointProbe::Invalid => Err(format!("agent 响应无法解析：{addr}{AGENT_HEALTH_ENDPOINT}")),
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

    // Connection: close 让 agent 写完响应后主动关闭连接；读到 EOF 才能拿到完整
    // body（旧实现只读到首个 \r\n 就断，version/local_token_path 这类字段还没
    // 传完就被截断，parse_health_version 永远拿不到值）。
    let mut response = Vec::with_capacity(256);
    let mut buf = [0_u8; 512];
    loop {
        match stream.read(&mut buf) {
            Ok(0) => break,
            Ok(n) => response.extend_from_slice(&buf[..n]),
            Err(_) => return EndpointProbe::Invalid,
        }
        // 防御异常大响应把内存吃满；探测端点的正常响应体很小。
        if response.len() >= 64 * 1024 {
            break;
        }
    }
    if response.is_empty() {
        return EndpointProbe::Invalid;
    }
    let response = String::from_utf8_lossy(&response);
    let Some(header_end) = response.find("\r\n\r\n") else {
        return EndpointProbe::Invalid;
    };
    let (head, body) = response.split_at(header_end);
    let body = body[4..].to_string();
    let Some(status) = head
        .lines()
        .next()
        .and_then(|line| line.split_whitespace().nth(1))
        .and_then(|value| value.parse::<u16>().ok())
    else {
        return EndpointProbe::Invalid;
    };
    EndpointProbe::Response { status, body }
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

    // fixture 支持携带任意 JSON body（用于 version/local_token_path 解析测试），
    // Content-Length 按 body 实际长度计算，不再写死 2。
    fn serve_statuses(statuses: Vec<(&'static str, u16, &'static str)>) -> String {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind fake agent");
        let addr = listener.local_addr().expect("fake agent addr").to_string();
        thread::spawn(move || {
            for (path, status, body) in statuses {
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
                    "HTTP/1.1 {status} {reason}\r\nContent-Length: {}\r\n\r\n{body}",
                    body.len()
                )
                .expect("write response");
                stream.flush().expect("flush response");
            }
        });
        addr
    }

    #[test]
    fn probe_reports_compatible_when_agent_health_exists() {
        let addr = serve_statuses(vec![(AGENT_HEALTH_ENDPOINT, 200, "[]")]);

        let outcome = probe_agent_health(&addr, Duration::from_secs(1));

        assert_eq!(outcome, ProbeOutcome::Compatible { version: None });
    }

    #[test]
    fn probe_parses_version_from_health_body() {
        let addr = serve_statuses(vec![(
            AGENT_HEALTH_ENDPOINT,
            200,
            r#"{"version":"9.9.9","provision_state":"open"}"#,
        )]);

        let outcome = probe_agent_health(&addr, Duration::from_secs(1));

        assert_eq!(
            outcome,
            ProbeOutcome::Compatible {
                version: Some("9.9.9".to_string())
            }
        );
    }

    #[test]
    fn parse_health_version_tolerates_bad_body() {
        assert_eq!(parse_health_version("not json"), None);
        assert_eq!(
            parse_health_version(r#"{"version":"1.2.3"}"#),
            Some("1.2.3".to_string())
        );
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
        let addr = serve_statuses(vec![(AGENT_HEALTH_ENDPOINT, 404, "[]")]);

        let outcome = probe_agent_health(&addr, Duration::from_secs(1));

        assert_eq!(
            outcome,
            ProbeOutcome::Incompatible {
                endpoint: AGENT_HEALTH_ENDPOINT,
                status: 404,
            }
        );
    }

    #[test]
    fn prepare_agent_port_recovers_incompatible_agent_before_starting_sidecar() {
        let addr = serve_statuses(vec![(AGENT_HEALTH_ENDPOINT, 404, "[]")]);
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
                        endpoint: AGENT_HEALTH_ENDPOINT,
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
    fn prepare_agent_port_attaches_to_existing_compatible_agent() {
        let addr = serve_statuses(vec![(
            AGENT_HEALTH_ENDPOINT,
            200,
            r#"{"version":"2.0.0","provision_state":"open"}"#,
        )]);
        let mut recovery_calls = 0;

        let state = prepare_agent_port(
            &addr,
            Duration::from_secs(1),
            Duration::from_secs(1),
            |_, _| {
                recovery_calls += 1;
                Ok(true)
            },
        )
        .expect("attach to compatible agent");

        assert_eq!(
            state,
            AgentPortState::AttachExisting {
                version: Some("2.0.0".to_string())
            }
        );
        assert_eq!(
            recovery_calls, 0,
            "compatible agent must be attached, never stopped"
        );
    }

    #[test]
    fn prepare_agent_port_reports_incompatible_agent_when_recovery_is_not_safe() {
        let addr = serve_statuses(vec![(AGENT_HEALTH_ENDPOINT, 404, "[]")]);

        let err = prepare_agent_port(
            &addr,
            Duration::from_secs(1),
            Duration::from_secs(1),
            |_, _| Ok(false),
        )
        .expect_err("unsafe recovery should keep compatibility error");

        assert!(err.contains(&format!("{AGENT_HEALTH_ENDPOINT} 返回 404")));
    }

    #[test]
    fn stop_leaves_attached_agent_running() {
        let mut terminate_calls = 0;

        stop_with_mode_gate(
            Some(&AgentMode::Attached {
                version: Some("1.0.0".to_string()),
            }),
            || terminate_calls += 1,
        );

        assert_eq!(terminate_calls, 0, "attach 模式不应触发终止子进程流程");
    }

    #[test]
    fn graceful_terminate_skips_kill_when_process_exits_in_time() {
        use std::cell::Cell;
        let alive_polls = Cell::new(0_u32);
        let killed = Cell::new(false);

        // 第 2 次探活时报告已退出，模拟 SIGTERM 后进程及时退出。
        let outcome = terminate_pid_gracefully(
            1234,
            3,
            || {},
            || {
                let n = alive_polls.get() + 1;
                alive_polls.set(n);
                n < 2
            },
            || killed.set(true),
        );

        assert_eq!(outcome, TerminateOutcome::ExitedAfterTerm);
        assert!(!killed.get(), "及时退出不应再 SIGKILL");
    }

    #[test]
    fn graceful_terminate_force_kills_after_timeout() {
        use std::cell::Cell;
        let killed = Cell::new(false);

        // 探活恒为存活，模拟 agent 未在超时内退出。
        let outcome = terminate_pid_gracefully(1234, 2, || {}, || true, || killed.set(true));

        assert_eq!(outcome, TerminateOutcome::ForceKilled);
        assert!(killed.get(), "超时后必须 SIGKILL 兜底");
    }

    #[test]
    fn agent_sidecar_log_rotates_and_persists_stderr() {
        let root = temp_test_dir("sidecar-log");
        let current = root.join(AGENT_SIDECAR_LOG_FILE);
        fs::write(&current, "previous startup failure\n").expect("write previous log");

        let mut file = open_agent_sidecar_log(&root, 8).expect("open rotated sidecar log");
        write_agent_sidecar_log(&mut file, "stderr", "sample initialization failed")
            .expect("persist sidecar stderr");
        drop(file);

        assert_eq!(
            fs::read_to_string(root.join(AGENT_SIDECAR_LOG_ROTATED_FILE))
                .expect("read rotated log"),
            "previous startup failure\n"
        );
        assert_eq!(
            fs::read_to_string(current).expect("read current log"),
            "[stderr] sample initialization failed\n"
        );
        fs::remove_dir_all(root).expect("cleanup temp test dir");
    }
}
