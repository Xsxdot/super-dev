// Rust HTTP pipeline example.
//
// Responsibilities:
//   - Expose /health for deployment health checks.
//   - Expose /info with language and app metadata.
//
// Boundaries:
//   - Uses only the Rust standard library.
//   - Does not depend on a reverse proxy.

use std::env;
use std::io::{Read, Write};
use std::net::{TcpListener, TcpStream};

fn main() {
    let port = env::var("PORT").unwrap_or_else(|_| "18084".to_string());
    let listener = TcpListener::bind(format!("0.0.0.0:{port}")).expect("bind listener");
    for stream in listener.incoming().flatten() {
        handle(stream);
    }
}

fn handle(mut stream: TcpStream) {
    let mut buffer = [0; 1024];
    let _ = stream.read(&mut buffer);
    let request = String::from_utf8_lossy(&buffer);
    if request.starts_with("GET /health ") {
        respond(&mut stream, "200 OK", "text/plain", "ok");
        return;
    }
    if request.starts_with("GET /info ") {
        let version = env::var("APP_VERSION").unwrap_or_default();
        respond(
            &mut stream,
            "200 OK",
            "application/json",
            &format!(r#"{{"app":"rust-http","language":"rust","version":"{version}"}}"#),
        );
        return;
    }
    respond(&mut stream, "200 OK", "text/plain", "rust-http");
}

fn respond(stream: &mut TcpStream, status: &str, content_type: &str, body: &str) {
    let response = format!(
        "HTTP/1.1 {status}\r\ncontent-type: {content_type}\r\ncontent-length: {}\r\n\r\n{body}",
        body.len()
    );
    let _ = stream.write_all(response.as_bytes());
}
