use std::sync::Mutex;

use tauri::AppHandle;
use tauri_plugin_shell::process::CommandChild;
use tauri_plugin_shell::ShellExt;

pub struct AgentProcess(pub Mutex<Option<CommandChild>>);

impl AgentProcess {
    pub fn new() -> Self {
        AgentProcess(Mutex::new(None))
    }

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

        let (_rx, child) = app
            .shell()
            .sidecar("superdev-agent")
            .map_err(|e| format!("找不到 agent sidecar: {e}"))?
            .args(["--addr", addr, "--data", &data_dir])
            .spawn()
            .map_err(|e| format!("启动 agent 失败: {e}"))?;

        println!("[SuperDev] agent started");
        *guard = Some(child);
        Ok(())
    }

    pub fn stop(&self) {
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(child) = guard.take() {
            let _ = child.kill();
            println!("[SuperDev] agent stopped");
        }
    }
}
