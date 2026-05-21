#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
use agent::AgentProcess;

use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};

use tauri::{
    menu::{Menu, MenuItem},
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

fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

/// show_main_window 显示并聚焦主窗口。
///
/// 供 Popover 等非主窗口通过 Tauri invoke 调用，
/// 点击"查看日志"等按钮时将主窗口带到前台。
#[tauri::command]
fn show_main_window(app: tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.set_focus();
    }
}

/// show_settings_window 显示主窗口并切换到设置页。
fn show_settings_window(app: &tauri::AppHandle) {
    if let Some(w) = app.get_webview_window("main") {
        let _ = w.show();
        let _ = w.eval("window.location.hash = '#/settings'");
        let _ = w.set_focus();
    }
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

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_autostart::init(
            MacosLauncher::LaunchAgent,
            None,
        ))
        .invoke_handler(tauri::generate_handler![show_main_window])
        .setup(|app| {
            let agent = AgentProcess::new();
            if let Err(e) = agent.start(app.handle()) {
                eprintln!("[SuperDev] {e}");
            }
            app.manage(agent);

            // 系统托盘（勿在 tauri.conf.json 再配置 trayIcon，否则会创建重复图标）
            let settings = MenuItem::with_id(app, "settings", "设置…", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出 SuperDev", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&settings, &quit])?;

            TrayIconBuilder::with_id("main")
                .icon(
                    app.default_window_icon()
                        .ok_or("未配置默认窗口图标")?
                        .clone(),
                )
                .icon_as_template(true)
                .show_menu_on_left_click(false)
                .menu(&menu)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "settings" => {
                        show_settings_window(app);
                    }
                    "quit" => {
                        app.state::<AgentProcess>().stop();
                        app.exit(0);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    match event {
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
                    }
                })
                .build(app)?;

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
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
