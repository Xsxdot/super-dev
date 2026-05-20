#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
use agent::AgentProcess;
use tauri::Manager;

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_shell::init())
        .setup(|app| {
            let agent = AgentProcess::new();
            #[cfg(not(debug_assertions))]
            {
                let resource_path = app
                    .path()
                    .resource_dir()
                    .unwrap()
                    .join("binaries/superdev-agent");
                agent.start(resource_path.to_str().unwrap());
            }
            app.manage(agent);
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::Destroyed = event {
                let agent = window.app_handle().state::<AgentProcess>();
                agent.stop();
            }
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
