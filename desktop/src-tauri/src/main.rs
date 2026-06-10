#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
mod mcp_install;
use agent::AgentProcess;
use mcp_install::{
    detect_coding_agents, install_mcp, mcp_docs, mcp_install_hint, mcp_status, uninstall_mcp,
};

use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};

use tauri::{
    menu::{AboutMetadata, Menu, MenuItem, PredefinedMenuItem, Submenu},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, Monitor, PhysicalPosition, PhysicalSize, WebviewUrl, WebviewWindowBuilder,
};
use tauri_plugin_autostart::MacosLauncher;

/// 最近一次显示 popover 的时间戳（ms），用于忽略打开瞬间的误触 Focused(false)。
static POPOVER_SHOWN_AT_MS: AtomicU64 = AtomicU64::new(0);

const POPOVER_WIDTH: i32 = 440;
const POPOVER_HEIGHT: i32 = 420;
const POPOVER_GAP: i32 = 8;
/// 打开后忽略失焦关闭的宽限期（macOS 菜单栏点击常会立刻触发一次失焦）。
const POPOVER_FOCUS_GRACE_MS: u64 = 250;
const LOOPBACK_PROXY_BYPASS: [&str; 3] = ["localhost", "127.0.0.1", "::1"];

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

/// reveal_main_window_from_aux_context 从 popover/tray/menu 等辅助上下文唤起主窗口。
///
/// macOS 菜单栏 popover 上下文不会自动激活应用；先隐藏置顶 popover，再显示、
/// 反最小化并聚焦主窗口，最后把应用激活为 Regular，保证视觉上真正回到前台。
fn reveal_main_window_from_aux_context(app: &tauri::AppHandle, route_hash: Option<&str>) {
    if let Some(popover) = app.get_webview_window("popover") {
        let _ = popover.hide();
    }

    if let Some(main) = app.get_webview_window("main") {
        let _ = main.unminimize();
        let _ = main.show();
        if let Some(hash) = route_hash {
            let script = format!("window.location.hash = '{}'", hash);
            let _ = main.eval(&script);
        }
        let _ = main.set_focus();
    }

    #[cfg(target_os = "macos")]
    {
        let _ = app.set_activation_policy(tauri::ActivationPolicy::Regular);
    }
}

/// show_main_window 显示并聚焦主窗口。
///
/// 供 Popover 等非主窗口通过 Tauri invoke 调用，
/// 点击"查看日志"等按钮时将主窗口带到前台。
#[tauri::command]
fn show_main_window(app: tauri::AppHandle) {
    reveal_main_window_from_aux_context(&app, None);
}

/// show_home_window 显示主窗口并切换到首页。
///
/// 系统通知点击后只负责把用户带回 SuperDev 首页，审批快捷操作继续由首页右下角
/// 现有浮层承载。
#[tauri::command]
fn show_home_window(app: tauri::AppHandle) {
    reveal_main_window_from_aux_context(&app, Some("#/"));
}

/// show_settings_window 显示主窗口并切换到设置页。
fn show_settings_window(app: &tauri::AppHandle) {
    reveal_main_window_from_aux_context(app, Some("#/settings"));
}

/// toggle_popover 切换 popover 窗口的显示/隐藏状态。
///
/// 左键点击托盘图标时调用：
/// - 已可见 → 隐藏
/// - 已隐藏或未创建 → 定位后显示
fn toggle_popover(app: &tauri::AppHandle, tray_rect: Option<tauri::Rect>) {
    if let Some(w) = app.get_webview_window("popover") {
        if w.is_visible().unwrap_or(false) {
            let _ = w.hide();
            return;
        }
        position_and_show_popover(app, &w, tray_rect);
        return;
    }

    // 首次创建
    let win = WebviewWindowBuilder::new(
        app,
        "popover",
        WebviewUrl::App("index.html#/popover".into()),
    )
    .title("")
    .inner_size(440.0, 420.0)
    .decorations(false)
    .always_on_top(true)
    .skip_taskbar(true)
    .visible(false)
    .build();

    match win {
        Ok(w) => position_and_show_popover(app, &w, tray_rect),
        Err(e) => eprintln!("[SuperDev] 创建 popover 窗口失败: {e}"),
    }
}

/// tray-icon 上报的 Rect 已是物理像素，scale_factor 用 1.0 即可（与 tauri-plugin-positioner 一致）。
fn tray_rect_physical(rect: &tauri::Rect) -> (PhysicalPosition<f64>, PhysicalSize<f64>) {
    let pos = rect.position.to_physical::<f64>(1.0);
    let size = rect.size.to_physical::<f64>(1.0);
    (pos, size)
}

/// 查找包含该全局物理坐标点的显示器（多屏时必须用，不能用 current_monitor）。
fn monitor_at_point(app: &tauri::AppHandle, x: f64, y: f64) -> Option<Monitor> {
    if let Ok(Some(m)) = app.monitor_from_point(x, y) {
        return Some(m);
    }
    if let Ok(monitors) = app.available_monitors() {
        for m in monitors {
            let p = m.position();
            let s = m.size();
            let left = p.x as f64;
            let top = p.y as f64;
            let right = left + s.width as f64;
            let bottom = top + s.height as f64;
            if x >= left && x < right && y >= top && y < bottom {
                return Some(m);
            }
        }
    }
    app.primary_monitor().ok().flatten()
}

/// 将 popover 左上角约束在指定显示器的 work_area 内。
fn clamp_to_work_area(wx: i32, wy: i32, monitor: &Monitor) -> (i32, i32) {
    let area = monitor.work_area();
    let left = area.position.x;
    let top = area.position.y;
    let right = left + area.size.width as i32;
    let bottom = top + area.size.height as i32;
    let pad = 4_i32;

    let mut wx = wx;
    let mut wy = wy;
    if wx < left {
        wx = left + pad;
    }
    if wx + POPOVER_WIDTH > right {
        wx = right - POPOVER_WIDTH - pad;
    }
    if wy < top {
        wy = top + pad;
    }
    if wy + POPOVER_HEIGHT > bottom {
        wy = bottom - POPOVER_HEIGHT - pad;
    }
    (wx, wy)
}

/// position_and_show_popover 将 popover 窗口定位到托盘图标下方并显示。
///
/// 使用托盘点击事件中的全局物理坐标 + monitor_from_point，避免多屏时跑到别的显示器。
fn position_and_show_popover(
    app: &tauri::AppHandle,
    window: &tauri::WebviewWindow,
    tray_rect: Option<tauri::Rect>,
) {
    let (x, y) = if let Some(rect) = tray_rect {
        let (pos, size) = tray_rect_physical(&rect);
        let tray_left = pos.x.round() as i32;
        let tray_top = pos.y.round() as i32;
        let tray_w = size.width.round() as i32;
        let tray_h = size.height.round() as i32;
        let tray_bottom = tray_top + tray_h;
        let cx = tray_left + tray_w / 2;

        let mut wx = cx - POPOVER_WIDTH / 2;
        let mut wy = tray_bottom + POPOVER_GAP;

        let anchor_x = cx as f64;
        let anchor_y = tray_bottom as f64;

        if let Some(monitor) = monitor_at_point(app, anchor_x, anchor_y) {
            let area = monitor.work_area();
            let work_bottom = area.position.y + area.size.height as i32;
            // 菜单栏在顶部：下方放不下则显示在图标上方
            if wy + POPOVER_HEIGHT > work_bottom {
                wy = tray_top - POPOVER_HEIGHT - POPOVER_GAP;
            }
            (wx, wy) = clamp_to_work_area(wx, wy, &monitor);
        }
        (wx, wy)
    } else if let Some(monitor) = app.primary_monitor().ok().flatten() {
        let area = monitor.work_area();
        let right = area.position.x + area.size.width as i32;
        (right - POPOVER_WIDTH - 4, area.position.y + 4)
    } else {
        (800, 30)
    };

    let _ = window.set_position(PhysicalPosition::new(x, y));
    let _ = window.show();
    POPOVER_SHOWN_AT_MS.store(now_ms(), Ordering::SeqCst);
    // 不 set_focus：避免菜单栏点击后立刻失焦导致刚打开就被隐藏
}

fn handle_menu_event(app: &tauri::AppHandle, id: &str) {
    match id {
        "settings" => show_settings_window(app),
        "quit" => {
            app.state::<AgentProcess>().stop();
            app.exit(0);
        }
        _ => {}
    }
}

fn append_loopback_proxy_bypass(existing: Option<&str>) -> String {
    let mut entries: Vec<String> = existing
        .unwrap_or("")
        .split(',')
        .map(str::trim)
        .filter(|entry| !entry.is_empty())
        .map(ToOwned::to_owned)
        .collect();

    for loopback in LOOPBACK_PROXY_BYPASS {
        if !entries.iter().any(|entry| entry == loopback) {
            entries.push(loopback.to_string());
        }
    }
    entries.join(",")
}

fn install_loopback_proxy_bypass() {
    let existing = match (
        std::env::var("NO_PROXY").ok(),
        std::env::var("no_proxy").ok(),
    ) {
        (Some(upper), Some(lower)) if upper != lower => Some(format!("{upper},{lower}")),
        (Some(value), _) | (_, Some(value)) => Some(value),
        (None, None) => None,
    };
    let bypass = append_loopback_proxy_bypass(existing.as_deref());

    // 桌面端和 agent 只通过本机回环地址通信；这些请求被系统/环境代理接管时，
    // WebView fetch 会报 Load failed，导致 onboarding 和设置保存无法继续。
    std::env::set_var("NO_PROXY", &bypass);
    std::env::set_var("no_proxy", bypass);
}

fn install_app_menu(app: &tauri::App) -> tauri::Result<()> {
    let about = PredefinedMenuItem::about(
        app,
        Some("关于 SuperDev"),
        Some(AboutMetadata {
            name: Some("SuperDev".to_string()),
            version: Some(env!("CARGO_PKG_VERSION").to_string()),
            ..Default::default()
        }),
    )?;
    let settings = MenuItem::with_id(app, "settings", "设置…", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出 SuperDev", true, Some("CmdOrCtrl+Q"))?;
    let app_menu = Submenu::with_items(
        app,
        "SuperDev",
        true,
        &[
            &about,
            &PredefinedMenuItem::separator(app)?,
            &settings,
            &PredefinedMenuItem::separator(app)?,
            &quit,
        ],
    )?;
    let edit_menu = Submenu::with_items(
        app,
        "编辑",
        true,
        &[
            &PredefinedMenuItem::undo(app, Some("撤销"))?,
            &PredefinedMenuItem::redo(app, Some("重做"))?,
            &PredefinedMenuItem::separator(app)?,
            &PredefinedMenuItem::cut(app, Some("剪切"))?,
            &PredefinedMenuItem::copy(app, Some("复制"))?,
            &PredefinedMenuItem::paste(app, Some("粘贴"))?,
            &PredefinedMenuItem::select_all(app, Some("全选"))?,
        ],
    )?;
    let window_menu = Submenu::with_items(
        app,
        "窗口",
        true,
        &[
            &PredefinedMenuItem::minimize(app, Some("最小化"))?,
            &PredefinedMenuItem::close_window(app, Some("关闭窗口"))?,
        ],
    )?;
    let help_menu = Submenu::with_items(app, "帮助", true, &[])?;
    let menu = Menu::with_items(app, &[&app_menu, &edit_menu, &window_menu, &help_menu])?;
    app.set_menu(menu)?;
    Ok(())
}

fn install_tray(app: &tauri::App) -> tauri::Result<()> {
    // 系统托盘（勿在 tauri.conf.json 再配置 trayIcon，否则会创建重复图标）
    let settings = MenuItem::with_id(app, "settings", "设置…", true, None::<&str>)?;
    let quit = MenuItem::with_id(app, "quit", "退出 SuperDev", true, None::<&str>)?;
    let menu = Menu::with_items(app, &[&settings, &quit])?;

    // 菜单栏用专用彩色图标（按 superdev-logo-v5-launch.svg 设计），不用 app icon
    let tray_icon = tauri::image::Image::from_bytes(include_bytes!("../icons/tray-icon.png"))?;

    TrayIconBuilder::with_id("main")
        .icon(tray_icon)
        .icon_as_template(false)
        .show_menu_on_left_click(false)
        .menu(&menu)
        .on_menu_event(|app, event| handle_menu_event(app, event.id.as_ref()))
        .on_tray_icon_event(|tray, event| match event {
            // 左键抬起 → 切换 Popover（左键不弹出菜单，见 show_menu_on_left_click(false)）
            TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                rect,
                ..
            } => {
                toggle_popover(tray.app_handle(), Some(rect));
            }
            // 右键按下时系统会弹出菜单；先收起 Popover
            TrayIconEvent::Click {
                button: MouseButton::Right,
                button_state: MouseButtonState::Down,
                ..
            } => {
                if let Some(w) = tray.app_handle().get_webview_window("popover") {
                    let _ = w.hide();
                }
            }
            _ => {}
        })
        .build(app)?;

    Ok(())
}

fn main() {
    install_loopback_proxy_bypass();

    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_notification::init())
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            None,
        ))
        .invoke_handler(tauri::generate_handler![
            show_main_window,
            show_home_window,
            detect_coding_agents,
            install_mcp,
            mcp_install_hint,
            mcp_status,
            uninstall_mcp,
            mcp_docs
        ])
        .on_menu_event(|app, event| handle_menu_event(app, event.id.as_ref()))
        .setup(|app| {
            let agent = AgentProcess::new();
            if let Err(e) = agent.start(app.handle()) {
                eprintln!("[SuperDev] agent failed to start: {e}");
            }
            app.manage(agent);
            if let Err(e) = install_app_menu(app) {
                eprintln!("[SuperDev] app menu failed to install: {e}");
            }
            if let Err(e) = install_tray(app) {
                eprintln!("[SuperDev] tray icon failed to install: {e}");
            }

            Ok(())
        })
        .on_window_event(|window, event| match event {
            tauri::WindowEvent::CloseRequested { api, .. } => {
                // 关闭时隐藏到托盘，仅对主窗口生效；popover 无关闭按钮
                if window.label() == "main" {
                    api.prevent_close();
                    let _ = window.hide();
                }
            }
            tauri::WindowEvent::Focused(false) => {
                if window.label() == "popover" {
                    let elapsed =
                        now_ms().saturating_sub(POPOVER_SHOWN_AT_MS.load(Ordering::SeqCst));
                    if elapsed < POPOVER_FOCUS_GRACE_MS {
                        return;
                    }
                    let _ = window.hide();
                }
            }
            _ => {}
        })
        .build(tauri::generate_context!())
        .expect("error while building tauri application")
        .run(|app, event| {
            // cmd+Q 或任何方式退出时都要 kill agent，避免遗留孤儿进程
            if let tauri::RunEvent::Exit = event {
                app.state::<AgentProcess>().stop();
            }
        });
}

#[cfg(test)]
mod tests {
    use super::append_loopback_proxy_bypass;

    const MAIN_RS: &str = include_str!("main.rs");

    fn install_app_menu_source() -> &'static str {
        let start = MAIN_RS
            .find("fn install_app_menu")
            .expect("main.rs should define install_app_menu");
        let relative_end = MAIN_RS[start..]
            .find("\nfn main()")
            .expect("install_app_menu should appear before main");
        &MAIN_RS[start..start + relative_end]
    }

    fn main_source() -> &'static str {
        let start = MAIN_RS
            .find("fn main()")
            .expect("main.rs should define main");
        let relative_end = MAIN_RS[start..]
            .find("\n#[cfg(test)]")
            .expect("test module should appear after main");
        &MAIN_RS[start..start + relative_end]
    }

    fn setup_source() -> &'static str {
        let source = main_source();
        let start = source
            .find(".setup(|app| {")
            .expect("main.rs should define setup callback");
        let relative_end = source[start..]
            .find("\n        .on_window_event")
            .expect("setup callback should appear before window events");
        &source[start..start + relative_end]
    }

    #[test]
    fn loopback_proxy_bypass_preserves_existing_domains() {
        assert_eq!(
            append_loopback_proxy_bypass(Some("example.com,localhost")),
            "example.com,localhost,127.0.0.1,::1"
        );
        assert_eq!(
            append_loopback_proxy_bypass(None),
            "localhost,127.0.0.1,::1"
        );
    }

    #[test]
    fn main_installs_loopback_proxy_bypass_before_tauri_builder() {
        let source = main_source();
        let bypass_pos = source
            .find("install_loopback_proxy_bypass();")
            .expect("main should install loopback proxy bypass");
        let builder_pos = source
            .find("tauri::Builder::default()")
            .expect("main should create tauri builder");
        assert!(
            bypass_pos < builder_pos,
            "proxy bypass must be configured before WebView and sidecar startup"
        );
    }

    #[test]
    fn app_menu_includes_standard_edit_commands_for_webview_text_inputs() {
        let menu_source = install_app_menu_source();
        assert!(
            menu_source.contains("\"编辑\""),
            "macOS WebView text inputs need an Edit menu so standard shortcuts like Cmd+V work"
        );
        for expected in [
            "PredefinedMenuItem::undo(app, Some(\"撤销\"))",
            "PredefinedMenuItem::redo(app, Some(\"重做\"))",
            "PredefinedMenuItem::cut(app, Some(\"剪切\"))",
            "PredefinedMenuItem::copy(app, Some(\"复制\"))",
            "PredefinedMenuItem::paste(app, Some(\"粘贴\"))",
            "PredefinedMenuItem::select_all(app, Some(\"全选\"))",
        ] {
            assert!(
                menu_source.contains(expected),
                "missing menu item: {expected}"
            );
        }
        assert!(
            menu_source.contains("&edit_menu"),
            "the top-level app menu should install the Edit submenu"
        );
    }

    #[test]
    fn app_menu_quit_registers_cmd_q_accelerator() {
        let menu_source = install_app_menu_source();
        assert!(
            menu_source
                .contains("MenuItem::with_id(app, \"quit\", \"退出 SuperDev\", true, Some(\"CmdOrCtrl+Q\"))"),
            "the app menu quit item must bind Cmd+Q so macOS can dispatch app quit instead of only supporting mouse clicks"
        );
    }

    #[test]
    fn approval_notification_plugin_is_registered() {
        let source = main_source();
        assert!(
            source.contains(".plugin(tauri_plugin_notification::init())"),
            "native approval reminders require the Tauri notification plugin"
        );
    }

    #[test]
    fn approval_notification_show_home_window_routes_to_main_home() {
        let source = main_source();
        assert!(
            MAIN_RS.contains("fn show_home_window(app: tauri::AppHandle)"),
            "main.rs should expose a command for notification clicks to reveal the home page"
        );
        assert!(
            MAIN_RS.contains("reveal_main_window_from_aux_context(&app, Some(\"#/\"))"),
            "notification clicks should return to the SuperDev home route"
        );
        assert!(
            source.contains("show_home_window,"),
            "show_home_window must be registered in the invoke handler"
        );
    }

    #[test]
    fn setup_keeps_desktop_open_when_agent_start_fails() {
        let source = setup_source();
        assert!(
            source.contains("if let Err(e) = agent.start(app.handle()) {"),
            "setup should explicitly handle agent startup failures"
        );
        assert!(
            source.contains("[SuperDev] agent failed to start"),
            "agent startup failures should be logged for crash diagnosis"
        );
        assert!(
            !source.contains("return Err(e.into());"),
            "agent startup failure should not abort the Tauri app during did_finish_launching"
        );
    }

    #[test]
    fn setup_keeps_desktop_open_when_menu_or_tray_setup_fails() {
        let source = setup_source();
        assert!(
            source.contains("if let Err(e) = install_app_menu(app) {"),
            "app menu setup should be handled as a recoverable startup step"
        );
        assert!(
            source.contains("[SuperDev] app menu failed to install"),
            "app menu failures should leave a diagnostic log"
        );
        assert!(
            source.contains("if let Err(e) = install_tray(app) {"),
            "tray setup should be handled as a recoverable startup step"
        );
        assert!(
            source.contains("[SuperDev] tray icon failed to install"),
            "tray failures should leave a diagnostic log"
        );
        assert!(
            !source.contains("install_app_menu(app)?"),
            "app menu failure should not abort the Tauri app during did_finish_launching"
        );
    }
}
