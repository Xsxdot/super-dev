use std::process::{Child, Command};
use std::sync::Mutex;

pub struct AgentProcess(pub Mutex<Option<Child>>);

impl AgentProcess {
    pub fn new() -> Self {
        AgentProcess(Mutex::new(None))
    }

    pub fn start(&self, sidecar_path: &str) {
        let mut guard = self.0.lock().unwrap();
        if guard.is_some() {
            return;
        }
        match Command::new(sidecar_path)
            .args(["--addr", ":27017"])
            .spawn()
        {
            Ok(child) => {
                *guard = Some(child);
                println!("[SuperDev] agent started, pid={}", guard.as_ref().unwrap().id());
            }
            Err(e) => eprintln!("[SuperDev] failed to start agent: {e}"),
        }
    }

    pub fn stop(&self) {
        let mut guard = self.0.lock().unwrap();
        if let Some(mut child) = guard.take() {
            let _ = child.kill();
            let _ = child.wait();
            println!("[SuperDev] agent stopped");
        }
    }
}
