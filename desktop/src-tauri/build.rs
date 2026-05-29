fn main() {
    // agent 编译见 scripts/build-agent.sh（beforeDevCommand / beforeBuildCommand 调用）。
    // 勿在 build.rs 里 go build：会改写 binaries/ 触发 tauri dev 无限重编译。
    tauri_build::build()
}
