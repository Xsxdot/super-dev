#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent;
use agent::AgentProcess;

use tauri::{
    menu::{Menu, MenuItem},
    tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent},
    Manager, PhysicalPosition, WebviewUrl, WebviewWindowBuilder,
};

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
        position_and_show_popover(&w, tray_rect);
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
        Ok(w) => position_and_show_popover(&w, tray_rect),
        Err(e) => eprintln!("[SuperDev] 创建 popover 窗口失败: {e}"),
    }
}

/// position_and_show_popover 将 popover 窗口定位到托盘图标下方并显示。
///
/// 坐标计算：图标水平居中，图标正下方 8pt 间距。
/// 若 tray_rect 为 None（极少数情况），fallback 到屏幕右上角。
/// 自动检测屏幕边界，防止窗口超出屏幕底部或右侧。
///
/// 注意：
///   - tauri::Rect 的 position/size 字段是 dpi::Position/Size 枚举，
///     需要通过 scale_factor 转换为物理像素坐标。
///     此处使用 scale_factor=1.0 因为托盘坐标已是物理像素。
fn position_and_show_popover(
    window: &tauri::WebviewWindow,
    tray_rect: Option<tauri::Rect>,
) {
    let popover_width: i32 = 440;
    let popover_height: i32 = 420;

    // scale_factor=1.0: 托盘坐标已是物理像素，直接转换不需要缩放
    let scale_factor = 1.0_f64;

    let (x, y) = if let Some(rect) = tray_rect {
        let pos = rect.position.to_physical::<f64>(scale_factor);
        let size = rect.size.to_physical::<f64>(scale_factor);
        let cx = (pos.x + size.width / 2.0) as i32;
        let bottom = (pos.y + size.height) as i32;
        let mut wx = cx - popover_width / 2;
        let mut wy = bottom + 8;

        if let Ok(Some(m)) = window.current_monitor() {
            let screen_h = m.size().height as i32;
            let screen_w = m.size().width as i32;
            if wy + popover_height > screen_h {
                wy = (pos.y as i32) - popover_height - 8;
            }
            if wx + popover_width > screen_w {
                wx = screen_w - popover_width - 4;
            }
            if wx < 0 {
                wx = 4;
            }
        }
        (wx, wy)
    } else {
        if let Ok(Some(m)) = window.current_monitor() {
            let sw = m.size().width as i32;
            (sw - popover_width - 4, 30)
        } else {
            (800, 30)
        }
    };

    let _ = window.set_position(PhysicalPosition::new(x, y));
    let _ = window.show();
    let _ = window.set_focus();
}

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_shell::init())
        .invoke_handler(tauri::generate_handler![show_main_window])
        .setup(|app| {
            let agent = AgentProcess::new();
            if let Err(e) = agent.start(app.handle()) {
                eprintln!("[SuperDev] {e}");
            }
            app.manage(agent);

            // 系统托盘
            let show = MenuItem::with_id(app, "show", "显示主窗口", true, None::<&str>)?;
            let quit = MenuItem::with_id(app, "quit", "退出 SuperDev", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&show, &quit])?;

            TrayIconBuilder::new()
                .icon(
                    app.default_window_icon()
                        .ok_or("未配置默认窗口图标")?
                        .clone(),
                )
                .menu(&menu)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "show" => {
                        if let Some(w) = app.get_webview_window("main") {
                            let _ = w.show();
                            let _ = w.set_focus();
                        }
                    }
                    "quit" => {
                        app.state::<AgentProcess>().stop();
                        app.exit(0);
                    }
                    _ => {}
                })
                .on_tray_icon_event(|tray, event| {
                    // 左键抬起 → toggle popover
                    if let TrayIconEvent::Click {
                        button: MouseButton::Left,
                        button_state: MouseButtonState::Up,
                        ..
                    } = event
                    {
                        // tray.rect() 返回 Result<Option<Rect>>，忽略错误时用 None
                        let rect = tray.rect().unwrap_or(None);
                        toggle_popover(tray.app_handle(), rect);
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
                    let _ = window.hide();
                }
            }
            _ => {}
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
