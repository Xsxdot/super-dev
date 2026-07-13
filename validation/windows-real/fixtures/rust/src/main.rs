//! Rust Windows validation fixture.
//!
//! 职责：用真实 Rust 原生进程提供 readiness、campaign Bearer probe、受控错误、结构化日志和稳定断点变量。
//! 边界：仅依赖 Rust 标准库与 Windows 控制台 API；不调用 SuperDev/MCP，也不记录凭据。

use std::collections::HashMap;
use std::env;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread;
use std::time::Duration;

const PROVIDER: &str = "rust";
const CONTRACT_VERSION: &str = "v1";
const DEFAULT_PORT: u16 = 18175;
const MAX_REQUEST_BYTES: usize = 64 * 1024;
static RUNNING: AtomicBool = AtomicBool::new(true);

#[cfg(windows)]
#[link(name = "Kernel32")]
extern "system" {
    fn SetConsoleCtrlHandler(
        handler: Option<unsafe extern "system" fn(u32) -> i32>,
        add: i32,
    ) -> i32;
}

#[cfg(windows)]
unsafe extern "system" fn console_handler(control: u32) -> i32 {
    // Windows 的 CTRL_C/CTRL_BREAK/CTRL_CLOSE 只置停止位，主循环负责正常释放 listener。
    if matches!(control, 0 | 1 | 2) {
        RUNNING.store(false, Ordering::SeqCst);
        return 1;
    }
    0
}

/// ProbeResult 保存断点可见的非秘密 Rust 局部变量。
struct ProbeResult {
    fixture_marker: &'static str,
    fixture_count: i64,
    fixture_provider: &'static str,
}

/// 启动 loopback server，并在 Windows 控制台停止事件后干净退出。
fn main() {
    if env::var("FIXTURE_STARTUP_MODE").ok().as_deref() == Some("fail") {
        write_log(
            "error",
            "fixture_startup_failed",
            &[("reason", "controlled_startup_failure".into())],
        );
        std::process::exit(23);
    }
    if env::var("FIXTURE_CAMPAIGN_ID")
        .ok()
        .filter(|value| !value.trim().is_empty())
        .is_none()
    {
        write_log(
            "error",
            "fixture_startup_failed",
            &[
                ("stage", "configuration".into()),
                ("cause", "FIXTURE_CAMPAIGN_ID is required".into()),
            ],
        );
        std::process::exit(24);
    }
    let port = match env_or_default("FIXTURE_PORT", &DEFAULT_PORT.to_string()).parse::<u16>() {
        Ok(value) => value,
        Err(error) => {
            write_log(
                "error",
                "fixture_startup_failed",
                &[("stage", "parse_port".into()), ("cause", error.to_string())],
            );
            std::process::exit(24);
        }
    };
    #[cfg(windows)]
    unsafe {
        if SetConsoleCtrlHandler(Some(console_handler), 1) == 0 {
            write_log(
                "error",
                "fixture_startup_failed",
                &[("stage", "register_console_handler".into())],
            );
            std::process::exit(24);
        }
    }
    let listener = match TcpListener::bind(("127.0.0.1", port)) {
        Ok(value) => value,
        Err(error) => {
            write_log(
                "error",
                "fixture_startup_failed",
                &[
                    ("stage", "listen".into()),
                    ("port", port.to_string()),
                    ("cause", error.to_string()),
                ],
            );
            std::process::exit(24);
        }
    };
    if let Err(error) = listener.set_nonblocking(true) {
        write_log(
            "error",
            "fixture_startup_failed",
            &[
                ("stage", "set_nonblocking".into()),
                ("cause", error.to_string()),
            ],
        );
        std::process::exit(24);
    }
    write_log(
        "info",
        "fixture_started",
        &[
            ("host", "127.0.0.1".into()),
            ("port", port.to_string()),
            ("contract_version", CONTRACT_VERSION.into()),
        ],
    );
    while RUNNING.load(Ordering::SeqCst) {
        match listener.accept() {
            Ok((stream, _)) => {
                // 验证流量是串行的；同步处理避免 cleanup 时留下脱离主循环的连接线程。
                if let Err(error) = handle_client(stream) {
                    write_log(
                        "error",
                        "fixture_request_failed",
                        &[("reason", "unexpected_error".into()), ("cause", error)],
                    );
                }
            }
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                thread::sleep(Duration::from_millis(50))
            }
            Err(error) => {
                write_log(
                    "error",
                    "fixture_run_failed",
                    &[("stage", "accept".into()), ("cause", error.to_string())],
                );
                std::process::exit(25);
            }
        }
    }
    write_log(
        "info",
        "fixture_stopping",
        &[("signal", "windows_console".into())],
    );
    drop(listener);
    write_log(
        "info",
        "fixture_stopped",
        &[("signal", "windows_console".into())],
    );
}

/// 处理一条 HTTP 连接，错误仅返回给调用线程并由结构化日志记录。
fn handle_client(mut stream: TcpStream) -> Result<(), String> {
    stream
        .set_read_timeout(Some(Duration::from_secs(5)))
        .map_err(|error| error.to_string())?;
    let (method, path, headers, body) = read_request(&mut stream)?;
    if method == "GET" && path == "/healthz" {
        write_response(
            &mut stream,
            200,
            "{\"ready\":true,\"contract_version\":\"v1\",\"provider\":\"rust\"}",
        )?;
        write_log(
            "info",
            "fixture_readiness_succeeded",
            &[("status", "200".into())],
        );
        return Ok(());
    }
    if method != "POST" || path != "/api/probe" {
        write_response(
            &mut stream,
            404,
            "{\"ok\":false,\"code\":\"fixture_not_found\",\"provider\":\"rust\"}",
        )?;
        write_log(
            "error",
            "fixture_request_rejected",
            &[
                ("reason", "route_not_found".into()),
                ("status", "404".into()),
            ],
        );
        return Ok(());
    }
    if !is_authorized(headers.get("authorization").map(String::as_str)) {
        write_response(
            &mut stream,
            401,
            "{\"ok\":false,\"code\":\"fixture_unauthorized\",\"provider\":\"rust\"}",
        )?;
        write_log(
            "error",
            "fixture_request_rejected",
            &[("reason", "unauthorized".into()), ("status", "401".into())],
        );
        return Ok(());
    }
    let trace_id = json_string(&body, "trace_id").unwrap_or_default();
    let request_id = json_string(&body, "request_id").unwrap_or_default();
    if trace_id.is_empty() || request_id.is_empty() {
        write_response(
            &mut stream,
            400,
            "{\"ok\":false,\"code\":\"fixture_invalid_request\",\"provider\":\"rust\"}",
        )?;
        write_log(
            "error",
            "fixture_request_failed",
            &[
                ("reason", "correlation_id_required".into()),
                ("status", "400".into()),
            ],
        );
        return Ok(());
    }
    let outcome = if json_string(&body, "outcome").as_deref() == Some("error") {
        "error"
    } else {
        "ok"
    };
    let value = json_integer(&body, "value").unwrap_or(41);
    write_log(
        "info",
        "fixture_request_started",
        &[
            ("trace_id", trace_id.clone()),
            ("request_id", request_id.clone()),
            ("outcome", outcome.into()),
        ],
    );
    let probe = fixture_probe(value);
    let (status, code, ok) = if outcome == "error" {
        (500, "fixture_controlled_error", false)
    } else {
        (200, "fixture_ok", true)
    };
    let response = format!(
        "{{\"ok\":{},\"code\":\"{}\",\"provider\":\"rust\",\"trace_id\":\"{}\",\"request_id\":\"{}\",\"result\":{}}}",
        ok,
        code,
        json_escape(&trace_id),
        json_escape(&request_id),
        probe.fixture_count
    );
    // 读取这些字段可阻止优化器在断点前后提前删除公共变量。
    let _breakpoint_identity = (probe.fixture_marker, probe.fixture_provider);
    write_response(&mut stream, status, &response)?;
    write_log(
        if status == 500 { "error" } else { "info" },
        "fixture_request_completed",
        &[
            ("trace_id", trace_id),
            ("request_id", request_id),
            ("outcome", outcome.into()),
            ("status", status.to_string()),
        ],
    );
    Ok(())
}

/// 创建稳定断点现场；所有变量都是非秘密 fixture 常量或派生数字。
#[inline(never)]
fn fixture_probe(value: i64) -> ProbeResult {
    let fixture_marker = "breakpoint-visible";
    let fixture_count = value + 1;
    let fixture_provider = PROVIDER;
    // SUPERDEV_FIXTURE_BREAKPOINT：变量已赋值且函数尚未返回，适合 lldb-dap 检查。
    ProbeResult {
        fixture_marker,
        fixture_count,
        fixture_provider,
    }
}

/// 读取有大小上限的 HTTP/1.1 请求并拆分头与 body。
fn read_request(
    stream: &mut TcpStream,
) -> Result<(String, String, HashMap<String, String>, String), String> {
    let mut bytes = Vec::new();
    let mut buffer = [0_u8; 4096];
    loop {
        let count = stream
            .read(&mut buffer)
            .map_err(|error| error.to_string())?;
        if count == 0 {
            break;
        }
        bytes.extend_from_slice(&buffer[..count]);
        if bytes.len() > MAX_REQUEST_BYTES {
            return Err("request exceeds 64 KiB".into());
        }
        if let Some(header_end) = find_bytes(&bytes, b"\r\n\r\n") {
            let headers_text = String::from_utf8_lossy(&bytes[..header_end]);
            let content_length = headers_text
                .lines()
                .find_map(|line| {
                    line.split_once(':')
                        .filter(|(name, _)| name.eq_ignore_ascii_case("content-length"))
                        .and_then(|(_, value)| value.trim().parse::<usize>().ok())
                })
                .unwrap_or(0);
            if bytes.len() >= header_end + 4 + content_length {
                break;
            }
        }
    }
    let header_end =
        find_bytes(&bytes, b"\r\n\r\n").ok_or_else(|| "malformed HTTP headers".to_string())?;
    let head =
        String::from_utf8(bytes[..header_end].to_vec()).map_err(|error| error.to_string())?;
    let mut lines = head.split("\r\n");
    let mut request_line = lines.next().unwrap_or_default().split_whitespace();
    let method = request_line.next().unwrap_or_default().to_string();
    let path = request_line.next().unwrap_or_default().to_string();
    let mut headers = HashMap::new();
    for line in lines {
        if let Some((name, value)) = line.split_once(':') {
            headers.insert(name.trim().to_ascii_lowercase(), value.trim().to_string());
        }
    }
    let body =
        String::from_utf8(bytes[header_end + 4..].to_vec()).map_err(|error| error.to_string())?;
    Ok((method, path, headers, body))
}

/// 写入固定 HTTP JSON 响应并关闭连接。
fn write_response(stream: &mut TcpStream, status: u16, body: &str) -> Result<(), String> {
    let reason = match status {
        200 => "OK",
        400 => "Bad Request",
        401 => "Unauthorized",
        404 => "Not Found",
        500 => "Internal Server Error",
        _ => "Unknown",
    };
    let response = format!(
        "HTTP/1.1 {status} {reason}\r\nContent-Type: application/json; charset=utf-8\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    );
    stream
        .write_all(response.as_bytes())
        .map_err(|error| error.to_string())
}

/// 从非秘密 campaign ID 推导 Bearer 值并定长比较，不记录完整 header。
fn is_authorized(header: Option<&str>) -> bool {
    let Some(supplied) = header else {
        return false;
    };
    let Ok(campaign_id) = env::var("FIXTURE_CAMPAIGN_ID") else {
        return false;
    };
    let expected = format!("Bearer superdev-validation-{}", campaign_id.trim());
    constant_time_equal(supplied.trim().as_bytes(), expected.as_bytes())
}

/// 定长比较认证字段，避免普通字符串提前退出。
fn constant_time_equal(left: &[u8], right: &[u8]) -> bool {
    if left.len() != right.len() {
        return false;
    }
    left.iter()
        .zip(right)
        .fold(0_u8, |diff, (a, b)| diff | (a ^ b))
        == 0
}

/// 从简单 fixture JSON 输入中读取字符串字段。
fn json_string(input: &str, key: &str) -> Option<String> {
    let needle = format!("\"{key}\"");
    let rest = input.get(input.find(&needle)? + needle.len()..)?;
    let rest = rest.get(rest.find(':')? + 1..)?.trim_start();
    let quoted = rest.strip_prefix('"')?;
    let end = quoted.find('"')?;
    Some(quoted[..end].to_string())
}

/// 从简单 fixture JSON 输入中读取整数。
fn json_integer(input: &str, key: &str) -> Option<i64> {
    let needle = format!("\"{key}\"");
    let rest = input.get(input.find(&needle)? + needle.len()..)?;
    let rest = rest.get(rest.find(':')? + 1..)?.trim_start();
    let end = rest
        .find(|character: char| !character.is_ascii_digit() && character != '-')
        .unwrap_or(rest.len());
    rest[..end].parse().ok()
}

/// 查找字节分隔符，避免 HTTP body 中的非 UTF-8 数据污染 header 解析。
fn find_bytes(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    haystack
        .windows(needle.len())
        .position(|window| window == needle)
}

/// 写入结构化 JSON line；字段调用方必须排除凭据与 Authorization。
fn write_log(level: &str, event: &str, fields: &[(&str, String)]) {
    let mut line = format!(
        "{{\"level\":\"{}\",\"event\":\"{}\",\"provider\":\"rust\",\"campaign_id\":\"{}\"",
        json_escape(level),
        json_escape(event),
        json_escape(&env_or_default("FIXTURE_CAMPAIGN_ID", "standalone"))
    );
    for (key, value) in fields {
        line.push_str(&format!(
            ",\"{}\":\"{}\"",
            json_escape(key),
            json_escape(value)
        ));
    }
    line.push_str("}\n");
    let result = if level == "error" {
        std::io::stderr().lock().write_all(line.as_bytes())
    } else {
        std::io::stdout().lock().write_all(line.as_bytes())
    };
    if result.is_ok() {
        let _ = if level == "error" {
            std::io::stderr().flush()
        } else {
            std::io::stdout().flush()
        };
    }
}

/// JSON 转义普通诊断文本。
fn json_escape(value: &str) -> String {
    value
        .replace('\\', "\\\\")
        .replace('"', "\\\"")
        .replace('\r', "\\r")
        .replace('\n', "\\n")
}

/// 读取环境值；空值不覆盖 fixture-only 默认值。
fn env_or_default(name: &str, fallback: &str) -> String {
    env::var(name)
        .ok()
        .filter(|value| !value.is_empty())
        .unwrap_or_else(|| fallback.to_string())
}
