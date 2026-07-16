//! runtime-validation Rust fixture 的无依赖 HTTP 运行与 lldb-dap 断点合同。
//!
//! 职责：暴露 readiness、正常/受控错误 probe，并保留稳定局部变量。
//! 边界：不使用第三方 crate、不访问 SuperDev API，也不持久化 campaign secret。

use std::env;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};

fn main() {
    let port = env::var("FIXTURE_PORT").expect("FIXTURE_PORT is required");
    let listener = TcpListener::bind(format!("127.0.0.1:{port}")).expect("bind fixture");
    for stream in listener.incoming() {
        handle(stream.expect("accept fixture"));
    }
}

fn handle(mut stream: TcpStream) {
    let mut request = [0_u8; 2048];
    let size = stream.read(&mut request).unwrap_or(0);
    let request = String::from_utf8_lossy(&request[..size]);
    if request.starts_with("GET /healthz ") {
        write_response(&mut stream, 200, "{\"ready\":true,\"provider\":\"rust\"}");
        return;
    }
    if request.starts_with("POST /api/probe") {
        // LLDB 对 Rust &str 的顶层 value 只暴露类型和地址；使用稳定整数合同，
        // 仍真实验证三个局部变量，同时避免把 debugger 展示格式误当业务值。
        let fixture_marker = 101_i32;
        let fixture_count = 42_i32;
        let fixture_provider = 7_i32;
        std::hint::black_box((fixture_marker, fixture_count, fixture_provider)); // SUPERDEV_FIXTURE_BREAKPOINT
        let controlled_error = request.starts_with("POST /api/probe?mode=error ");
        let body = format!(
            "{{\"ok\":{},\"provider\":\"rust\",\"count\":{}}}",
            !controlled_error, fixture_count
        );
        write_response(&mut stream, if controlled_error { 500 } else { 200 }, &body);
        return;
    }
    write_response(&mut stream, 404, "{\"ok\":false}");
}

fn write_response(stream: &mut TcpStream, status: u16, body: &str) {
    let reason = if status == 200 {
        "OK"
    } else if status == 500 {
        "Internal Server Error"
    } else {
        "Not Found"
    };
    let response = format!("HTTP/1.1 {status} {reason}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
    stream
        .write_all(response.as_bytes())
        .expect("write fixture response");
}
