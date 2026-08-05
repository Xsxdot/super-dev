// process.rs 提供连接器使用的有界 argv 进程执行器。
//
// 职责：
//   - 以 argv 形式启动外部命令（不经 shell）
//   - 在超时内捕获有限体积的 stdout/stderr
//   - 超时后终止子进程并返回稳定错误码
//
// 边界：
//   - 不解析 shell 语法、不拼接命令字符串
//   - 不把 argv、stdout、stderr 或完整路径写入用户可见错误/结构化日志字段
//   - 不负责 OpenClaw 等具体 CLI 的业务语义
//
// 本模块与 fs_port 并列，是连接器的第二个副作用端口：
//   - fs_port::ConnectorFs   —— 文件读写
//   - command_port::CommandRunner —— 进程调用
// 两者一起构成 registry::ConnectorPorts。之所以从 connectors/process.rs 提到
// 这一层：远端实现（remote_command::RemoteAgentCommandRunner）住在 mcp_install
// 下，连接器私有模块对它不可见。

// stderr 等字段由调用方按需消费；保留完整输出摘要避免 API 半成品。
#![allow(dead_code)]

use crate::mcp_install::registry::ConnectorError;
use std::ffi::{OsStr, OsString};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::mpsc;
use std::thread;
use std::time::{Duration, Instant};

/// MAX_CAPTURED_BYTES 是每个输出流保留的最大字节数（64 KiB）。
pub(crate) const MAX_CAPTURED_BYTES: usize = 64 * 1024;

/// CommandSpec 描述一次 argv 级进程调用。
#[derive(Clone, Debug)]
pub(crate) struct CommandSpec {
    /// program 是可执行文件路径（不经 PATH shell 展开时由调用方解析）。
    pub program: PathBuf,
    /// args 是原始参数列表。
    pub args: Vec<OsString>,
    /// timeout 是等待子进程退出的最长时限。
    pub timeout: Duration,
    /// env 是附加环境变量（不清除继承环境）。
    pub env: Vec<(OsString, OsString)>,
}

impl CommandSpec {
    /// new 构造带默认 30s 超时、无附加环境的命令规格。
    ///
    /// 参数：
    ///   - program: 可执行文件
    ///   - args: 可迭代的参数序列
    pub(crate) fn new(
        program: impl Into<PathBuf>,
        args: impl IntoIterator<Item = impl Into<OsString>>,
    ) -> Self {
        Self {
            program: program.into(),
            args: args.into_iter().map(Into::into).collect(),
            timeout: Duration::from_secs(30),
            env: Vec::new(),
        }
    }

    /// with_timeout 覆盖等待时限。
    pub(crate) fn with_timeout(mut self, timeout: Duration) -> Self {
        self.timeout = timeout;
        self
    }

    /// with_env 追加一条环境变量。
    pub(crate) fn with_env(mut self, key: impl Into<OsString>, value: impl Into<OsString>) -> Self {
        self.env.push((key.into(), value.into()));
        self
    }
}

/// CommandOutput 是有界捕获后的进程输出摘要。
#[derive(Clone, Debug)]
pub(crate) struct CommandOutput {
    /// status_code 是进程退出码（被信号杀死时可能为 None）。
    pub status_code: Option<i32>,
    /// stdout 是截断后的标准输出（有损 UTF-8 替换）。
    pub stdout: String,
    /// stderr 是截断后的标准错误。
    pub stderr: String,
    /// truncated 表示任一输出流超过了捕获上限。
    pub truncated: bool,
}

impl CommandOutput {
    /// success 表示进程以 0 退出。
    pub(crate) fn success(&self) -> bool {
        self.status_code == Some(0)
    }
}

/// CommandRunner 抽象可注入的进程执行后端。
pub(crate) trait CommandRunner: Send + Sync {
    /// run 按规格执行命令并返回有界输出或稳定错误。
    fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError>;
}

/// SystemCommandRunner 使用 std::process::Command 的真实系统实现。
pub(crate) struct SystemCommandRunner;

impl CommandRunner for SystemCommandRunner {
    /// run 以 argv 启动子进程，分线程排空输出，并在超时后强制终止。
    fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError> {
        let started = Instant::now();
        let program_name = program_basename(&spec.program);
        let arg_count = spec.args.len();
        tracing::debug!(
            program = program_name.as_str(),
            arg_count,
            "connector command spawn starting"
        );

        let mut command = Command::new(&spec.program);
        command
            .args(&spec.args)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .stdin(Stdio::null());
        for (key, value) in &spec.env {
            command.env(key, value);
        }

        let mut child = match command.spawn() {
            Ok(child) => child,
            Err(error) => {
                tracing::error!(
                    program = program_name.as_str(),
                    arg_count,
                    error_code = "command_spawn_failed",
                    duration_ms = started.elapsed().as_millis() as u64,
                    "connector command spawn failed"
                );
                return Err(ConnectorError::new(
                    "command_spawn_failed",
                    format!("启动命令失败: {error}"),
                ));
            }
        };

        let stdout = child.stdout.take();
        let stderr = child.stderr.take();
        let (stdout_tx, stdout_rx) = mpsc::channel();
        let (stderr_tx, stderr_rx) = mpsc::channel();

        // 捕获上限后仍继续排空，防止子进程写满管道后死锁。
        if let Some(mut pipe) = stdout {
            thread::spawn(move || {
                let _ = stdout_tx.send(drain_capped(&mut pipe));
            });
        } else {
            let _ = stdout_tx.send(CappedStream {
                text: String::new(),
                truncated: false,
            });
        }
        if let Some(mut pipe) = stderr {
            thread::spawn(move || {
                let _ = stderr_tx.send(drain_capped(&mut pipe));
            });
        } else {
            let _ = stderr_tx.send(CappedStream {
                text: String::new(),
                truncated: false,
            });
        }

        let deadline = started + spec.timeout;
        let status = loop {
            match child.try_wait() {
                Ok(Some(status)) => break status,
                Ok(None) => {
                    if Instant::now() >= deadline {
                        let _ = child.kill();
                        match child.wait() {
                            Ok(_) => {
                                // 尽量回收输出线程，避免悬挂。
                                let _ = stdout_rx.recv_timeout(Duration::from_millis(200));
                                let _ = stderr_rx.recv_timeout(Duration::from_millis(200));
                                tracing::error!(
                                    program = program_name.as_str(),
                                    arg_count,
                                    error_code = "command_timeout",
                                    duration_ms = started.elapsed().as_millis() as u64,
                                    "connector command timed out"
                                );
                                return Err(ConnectorError::new("command_timeout", "命令执行超时"));
                            }
                            Err(error) => {
                                tracing::error!(
                                    program = program_name.as_str(),
                                    arg_count,
                                    error_code = "command_wait_failed",
                                    duration_ms = started.elapsed().as_millis() as u64,
                                    "connector command wait failed after timeout kill"
                                );
                                return Err(ConnectorError::new(
                                    "command_wait_failed",
                                    format!("等待命令结束失败: {error}"),
                                ));
                            }
                        }
                    }
                    thread::sleep(Duration::from_millis(10));
                }
                Err(error) => {
                    tracing::error!(
                        program = program_name.as_str(),
                        arg_count,
                        error_code = "command_wait_failed",
                        duration_ms = started.elapsed().as_millis() as u64,
                        "connector command wait failed"
                    );
                    return Err(ConnectorError::new(
                        "command_wait_failed",
                        format!("等待命令失败: {error}"),
                    ));
                }
            }
        };

        let stdout_stream = stdout_rx
            .recv()
            .map_err(|_| ConnectorError::new("command_output_failed", "读取标准输出失败"))?;
        let stderr_stream = stderr_rx
            .recv()
            .map_err(|_| ConnectorError::new("command_output_failed", "读取标准错误失败"))?;
        let truncated = stdout_stream.truncated || stderr_stream.truncated;
        let output = CommandOutput {
            status_code: status.code(),
            stdout: stdout_stream.text,
            stderr: stderr_stream.text,
            truncated,
        };

        if output.success() {
            tracing::info!(
                program = program_name.as_str(),
                arg_count,
                status_code = output.status_code,
                truncated,
                duration_ms = started.elapsed().as_millis() as u64,
                "connector command finished"
            );
        } else {
            tracing::warn!(
                program = program_name.as_str(),
                arg_count,
                status_code = output.status_code,
                truncated,
                duration_ms = started.elapsed().as_millis() as u64,
                "connector command exited nonzero"
            );
        }
        Ok(output)
    }
}

struct CappedStream {
    text: String,
    truncated: bool,
}

fn drain_capped(reader: &mut dyn Read) -> CappedStream {
    let mut buffer = [0_u8; 8 * 1024];
    let mut kept = Vec::with_capacity(MAX_CAPTURED_BYTES.min(8 * 1024));
    let mut truncated = false;
    loop {
        match reader.read(&mut buffer) {
            Ok(0) => break,
            Ok(n) => {
                if kept.len() < MAX_CAPTURED_BYTES {
                    let room = MAX_CAPTURED_BYTES - kept.len();
                    let take = room.min(n);
                    kept.extend_from_slice(&buffer[..take]);
                    if take < n {
                        truncated = true;
                    }
                } else {
                    // 已达上限仍继续读，避免管道阻塞导致子进程挂死。
                    truncated = true;
                }
            }
            Err(_) => break,
        }
    }
    CappedStream {
        text: String::from_utf8_lossy(&kept).into_owned(),
        truncated,
    }
}

fn program_basename(program: &Path) -> String {
    program
        .file_name()
        .and_then(OsStr::to_str)
        .unwrap_or("command")
        .to_string()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::{Arc, Mutex};

    fn rustc_program() -> PathBuf {
        PathBuf::from(env!("CARGO"))
            .parent()
            .expect("cargo parent")
            .join(if cfg!(windows) { "rustc.exe" } else { "rustc" })
    }

    #[cfg(unix)]
    fn sleep_spec(duration: Duration) -> CommandSpec {
        CommandSpec::new(
            "/bin/sh",
            [
                OsString::from("-c"),
                OsString::from(format!("sleep {}", duration.as_secs().max(1))),
            ],
        )
    }

    #[cfg(windows)]
    fn sleep_spec(duration: Duration) -> CommandSpec {
        CommandSpec::new(
            "powershell",
            [
                OsString::from("-NoProfile"),
                OsString::from("-Command"),
                OsString::from(format!(
                    "Start-Sleep -Seconds {}",
                    duration.as_secs().max(1)
                )),
            ],
        )
    }

    #[cfg(unix)]
    fn large_output_spec() -> CommandSpec {
        // 通过 argv 传入看似敏感的标记，断言错误/日志字段不会回显。
        CommandSpec::new(
            "/bin/sh",
            [
                OsString::from("-c"),
                OsString::from(
                    "secret=super-secret-token-xyz; \
                     dd if=/dev/zero bs=1024 count=80 2>/dev/null | tr '\\0' 'A'; \
                     echo; \
                     dd if=/dev/zero bs=1024 count=80 2>/dev/null | tr '\\0' 'B' 1>&2",
                ),
                OsString::from("super-secret-token-xyz"),
            ],
        )
        .with_timeout(Duration::from_secs(10))
    }

    #[cfg(windows)]
    fn large_output_spec() -> CommandSpec {
        CommandSpec::new(
            "powershell",
            [
                OsString::from("-NoProfile"),
                OsString::from("-NonInteractive"),
                OsString::from("-Command"),
                OsString::from(
                    "$a = [Text.Encoding]::ASCII.GetBytes('A' * (80 * 1024)); \
                     $b = [Text.Encoding]::ASCII.GetBytes('B' * (80 * 1024)); \
                     $stdout = [Console]::OpenStandardOutput(); \
                     $stderr = [Console]::OpenStandardError(); \
                     $stdout.Write($a, 0, $a.Length); $stderr.Write($b, 0, $b.Length)",
                ),
            ],
        )
        .with_timeout(Duration::from_secs(15))
    }

    #[test]
    fn system_runner_captures_success_without_shell_expansion() {
        let output = SystemCommandRunner
            .run(CommandSpec::new(rustc_program(), ["--version"]))
            .expect("rustc runs");
        assert!(output.success());
        assert!(output.stdout.contains("rustc"));
    }

    #[test]
    fn system_runner_times_out_and_kills_the_child() {
        let started = Instant::now();
        let spec = sleep_spec(Duration::from_secs(5)).with_timeout(Duration::from_millis(50));
        let error = SystemCommandRunner.run(spec).expect_err("must time out");
        assert_eq!(error.code(), "command_timeout");
        assert!(
            started.elapsed() < Duration::from_secs(1),
            "timeout must return quickly, elapsed={:?}",
            started.elapsed()
        );
        // 用户可见错误不得回显 argv。
        assert!(!error.message().contains("sleep"));
    }

    #[test]
    fn system_runner_caps_large_stdout_and_stderr_without_deadlock() {
        let output = SystemCommandRunner
            .run(large_output_spec())
            .expect("large output command must exit");
        assert!(
            output.truncated,
            "both streams exceed 64KiB: stdout={}, stderr={}",
            output.stdout.len(),
            output.stderr.len()
        );
        // 捕获上限按字节计；UTF-8 有损替换后长度仍不得超过上限。
        assert_eq!(output.stdout.len(), MAX_CAPTURED_BYTES);
        assert_eq!(output.stderr.len(), MAX_CAPTURED_BYTES);

        // 错误路径：用不存在程序证明用户消息不含密钥片段。
        let secret = "super-secret-token-xyz";
        let error = SystemCommandRunner
            .run(CommandSpec::new(
                if cfg!(windows) {
                    "C:\\nonexistent\\openclaw-missing.exe"
                } else {
                    "/nonexistent/openclaw-missing"
                },
                [secret],
            ))
            .expect_err("missing program");
        assert_eq!(error.code(), "command_spawn_failed");
        assert!(
            !error.message().contains(secret),
            "user-safe error must not include argv secret"
        );
        // 结构化日志字段同样不得包含 secret（此处只能断言错误对象）。
        let debug = format!("{error:?}");
        assert!(!debug.contains(secret));
    }

    /// RecordingRunner 仅用于测试注入，记录规格而不启动进程。
    #[derive(Clone, Default)]
    struct RecordingRunner {
        calls: Arc<Mutex<Vec<CommandSpec>>>,
    }

    impl CommandRunner for RecordingRunner {
        fn run(&self, spec: CommandSpec) -> Result<CommandOutput, ConnectorError> {
            self.calls.lock().expect("lock").push(spec);
            Ok(CommandOutput {
                status_code: Some(0),
                stdout: String::new(),
                stderr: String::new(),
                truncated: false,
            })
        }
    }

    #[test]
    fn command_spec_preserves_argv_and_env() {
        let runner = RecordingRunner::default();
        let spec = CommandSpec::new("openclaw", ["mcp", "show", "superdev"])
            .with_timeout(Duration::from_secs(5))
            .with_env("OPENCLAW_CONFIG_PATH", "/tmp/openclaw.json");
        runner.run(spec).unwrap();
        let calls = runner.calls.lock().unwrap();
        assert_eq!(calls.len(), 1);
        assert_eq!(calls[0].args.len(), 3);
        assert_eq!(calls[0].env.len(), 1);
        assert_eq!(calls[0].timeout, Duration::from_secs(5));
    }
}
