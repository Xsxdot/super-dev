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
        let fixture_marker = "breakpoint-visible";
        let fixture_count = 42;
        let fixture_provider = "rust";
        std::hint::black_box(fixture_marker); // SUPERDEV_FIXTURE_BREAKPOINT
        let controlled_error = request.starts_with("POST /api/probe?mode=error ");
        let body = format!("{{\"ok\":{},\"provider\":\"{}\",\"count\":{}}}", !controlled_error, fixture_provider, fixture_count);
        write_response(&mut stream, if controlled_error { 500 } else { 200 }, &body);
        return;
    }
    write_response(&mut stream, 404, "{\"ok\":false}");
}

fn write_response(stream: &mut TcpStream, status: u16, body: &str) {
    let reason = if status == 200 { "OK" } else if status == 500 { "Internal Server Error" } else { "Not Found" };
    let response = format!("HTTP/1.1 {status} {reason}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len());
    stream.write_all(response.as_bytes()).expect("write fixture response");
}
