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

        let (_rx, child) = app
            .shell()
            .sidecar("superdev-agent")
            .map_err(|e| format!("找不到 agent sidecar: {e}"))?
            .args(["--addr", "127.0.0.1:27017"])
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
