use std::process::{Child, Command};
use std::sync::Mutex;

pub struct AgentProcess(pub Mutex<Option<Child>>);

impl AgentProcess {
    pub fn new() -> Self {
        AgentProcess(Mutex::new(None))
    }

    pub fn start(&self, sidecar_path: &str) -> Result<(), std::io::Error> {
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if guard.is_some() {
            return Ok(());
        }
        let child = Command::new(sidecar_path)
            .args(["--addr", ":27017"])
            .spawn()?;
        println!("[SuperDev] agent started, pid={}", child.id());
        *guard = Some(child);
        Ok(())
    }

    pub fn stop(&self) {
        let mut guard = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if let Some(mut child) = guard.take() {
            let _ = child.kill();
            let _ = child.wait();
            println!("[SuperDev] agent stopped");
        }
    }
}
