// remote_fs.rs 提供 ConnectorFs 端口在远端机器上的实现。
//
// 职责：
//   - RemoteAgentFs 把 fs_port::ConnectorFs 的每个方法映射为一次到【本机】agent
//     的 HTTP 调用，经 Task 5 的 integrations 代理（`/api/agents/{host_id}/
//     integrations/{rest}`）转发到【目标机】agent 的受限文件端点（Task 3/4：
//     `/api/integrations/fs/*`）
//   - 把 Go 端点的响应形状翻译成 ConnectorFs 的语义（如 read 端点的
//     `{"exists":false}` → `Ok(None)`）
//   - 把调用方传入的 WriteLabels / 批量写 label 拼进错误文案，让远端安装的失败
//     提示与本地安装保持同一措辞习惯
//
// 边界：
//   - 不做路径白名单判断——白名单是目标机 Task 4 handler 的职责，在这里重复
//     实现只会与服务端漂移（见 fs_port.rs trait 文档同一纪律）
//   - 不持有、不转发任何跨机凭据：本结构只持有本机 agent 签发的 local access
//     token，只出现在 `Authorization: Bearer` 请求头里；目标机凭据由本机 agent
//     的 nodetransport 按 host_id 对应的 Agent Secret 独立注入，对本结构不可见
//   - 不解析任何智能体方言（配置怎么合并、skill 装到哪）——那是调用方（connector
//     安装逻辑）的职责，本结构只提供哑的文件原语
//   - 不在这里做重试/退避：一次方法调用只发一次 HTTP 请求，超时由 15s 预算兜底

use super::fs_port::{BatchFile, ConnectorFs, FsStat, WriteLabels};
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::time::Duration;

/// REMOTE_FS_TIMEOUT 是单次远端文件操作的总预算，与 Task 5 代理侧的
/// `integrationsProxyTimeout` 取值一致（write-batch 单批次内容上限 4MB，经
/// tunnel 链路冷启动握手可能明显长于普通小请求）。
const REMOTE_FS_TIMEOUT: Duration = Duration::from_secs(15);

/// RemoteAgentFs 是 ConnectorFs 端口在远端机器上的实现。
///
/// 数据流：桌面端 Rust（本结构） → HTTP 到【本机】agent
/// `/api/agents/{host_id}/integrations/{rest}`（Task 5 代理）→ 按 host_id 转发到
/// 【目标机】agent `/api/integrations/{rest}`（Task 3/4 端点）。本结构自始至终
/// 只跟本机 agent 说话，从不直接连接目标机器。
pub struct RemoteAgentFs {
    /// local_agent_base 是本机 agent 的完整 origin（如 "http://127.0.0.1:57017"，
    /// 参见 agent.rs 的 `agent_addr_and_data_dir`），可带或不带末尾斜杠。
    local_agent_base: String,
    /// local_token 是本机 agent 签发的 local access token，只用于
    /// `Authorization: Bearer` 请求头——**绝不能**出现在日志或错误串里。
    local_token: String,
    /// host_id 是目标机器在本机 agent `agents.json` 中的注册 ID。
    host_id: String,
    /// agent 是复用连接池、统一配置 15s 超时的 ureq HTTP 客户端。
    agent: ureq::Agent,
}

impl RemoteAgentFs {
    /// new 构造一个指向 host_id 的远端文件端口。
    ///
    /// 参数：
    ///   - local_agent_base: 本机 agent 的完整 origin（不含路径），例如
    ///     `"http://127.0.0.1:57017"`；本机回环连接，明文 HTTP 即可
    ///   - local_token: 本机 agent 的 local access token，仅用于鉴权本机代理
    ///     调用，不是目标机凭据
    ///   - host_id: 目标机器在本机 agent 里的注册 ID
    pub fn new(local_agent_base: String, local_token: String, host_id: String) -> Self {
        tracing::debug!(
            host_id = %host_id,
            local_agent_base = %local_agent_base,
            "constructing RemoteAgentFs"
        );
        Self {
            local_agent_base,
            local_token,
            host_id,
            agent: ureq::AgentBuilder::new().timeout(REMOTE_FS_TIMEOUT).build(),
        }
    }

    /// proxy_url 拼出到本机 agent 的完整 URL：
    /// `{base}/api/agents/{host_id}/integrations/{rest}`。
    fn proxy_url(&self, rest: &str) -> String {
        format!(
            "{}/api/agents/{}/integrations/{}",
            self.local_agent_base.trim_end_matches('/'),
            self.host_id,
            rest
        )
    }

    /// execute 发起一次到本机 agent 代理的 HTTP 调用，并把结果收敛成本模块统一
    /// 的错误文案风格。
    ///
    /// 参数：
    ///   - method/rest/query/json_body: 描述这次具体请求
    ///   - operation: 出现在错误文案里的动作名（如 "stat"、"write-batch"）
    ///   - path_context: 出现在 403 错误文案里的路径信息（rename 等双路径操作
    ///     由调用方自行拼成 "from -> to" 形式传入）
    ///
    /// 错误映射（对应 brief 要求的四类中的后三类；`{exists:false}` 那一类由
    /// read_optional 自己在拿到 200 响应体之后判断，不在这里处理）：
    ///   - HTTP 403：目标机白名单拒绝（Task 4 契约：白名单外一律 403），映射为
    ///     带路径的中文错误
    ///   - HTTP 502：Task 5 代理转发失败（`integration_target_unreachable`），
    ///     映射为「远端机器不可达」
    ///   - ureq 传输层错误（连不上本机 agent 本身）：同样映射为「远端机器不可达」
    ///     ——从调用方视角，本机 agent 代理不可用与目标机不可达同样意味着这次
    ///     远端文件操作做不成，不值得让上层区分这两种子原因
    ///   - 其它非 2xx：带 host_id 与操作名上下文的通用错误
    fn execute(
        &self,
        method: &str,
        rest: &str,
        query: Option<(&str, &str)>,
        json_body: Option<String>,
        operation: &str,
        path_context: &str,
    ) -> Result<String, String> {
        let url = self.proxy_url(rest);
        let mut request = self
            .agent
            .request(method, &url)
            .set("Authorization", &format!("Bearer {}", self.local_token));
        if let Some((key, value)) = query {
            request = request.query(key, value);
        }
        let outcome = match json_body {
            Some(body) => request
                .set("Content-Type", "application/json")
                .send_string(&body),
            None => request.call(),
        };
        match outcome {
            Ok(response) => response.into_string().map_err(|error| {
                format!(
                    "远端机器 {} 执行 {operation} 的响应无法读取: {error}",
                    self.host_id
                )
            }),
            Err(ureq::Error::Status(403, response)) => {
                // 白名单拒绝的响应体对调用方没有额外信息（只有稳定的
                // path_not_allowed code），无需解析，只需要把连接排空。
                let _ = response.into_string();
                Err(format!(
                    "远端机器 {} 拒绝访问该路径（不在允许范围内）: {path_context}",
                    self.host_id
                ))
            }
            Err(ureq::Error::Status(502, response)) => Err(format!(
                "远端机器 {} 不可达: {}",
                self.host_id,
                extract_error_message(response)
            )),
            Err(ureq::Error::Status(status, response)) => Err(format!(
                "远端机器 {} 执行 {operation} 失败（HTTP {status}）: {}",
                self.host_id,
                extract_error_message(response)
            )),
            Err(ureq::Error::Transport(transport)) => Err(format!(
                "远端机器 {} 不可达（无法连接本机 agent 完成 {operation}）: {transport}",
                self.host_id
            )),
        }
    }
}

/// StatResponse 对应 `GET fs/stat` 的响应体 `{exists, is_dir, size}`。
///
/// size 字段本结构不关心（FsStat 端口契约里没有这一位），不声明它——多余的
/// JSON key 会被 serde 直接忽略，不需要 `#[serde(default)]` 之类的样板。
#[derive(Deserialize)]
struct StatResponse {
    exists: bool,
    is_dir: bool,
}

/// ReadResponse 只建模 `content` 一个字段。
///
/// Go 端点契约：成功时响应体是 `{"content": "..."}`（没有 `exists` 字段）；目标
/// 不存在时是 `{"exists": false}`（没有 `content` 字段），状态码同样是 200。
/// serde 对结构体里 `Option<T>` 字段有特殊处理：JSON 缺这个 key 时自动得到
/// `None`，不需要写 `#[serde(default)]`——这正是这里唯一安全的写法。
///
/// **不要**加一个 `exists: Option<bool>` 字段再靠它判断存在性：成功响应压根
/// 没有 `exists` 这个 key，一旦被 `#[serde(default)]` 悄悄兜成 `false`，就会把
/// 「读取成功」误判成「文件不存在」，且不会有任何编译期或运行时报错——这是
/// brief 明确点名的坑。判定存在性只能用 `content.is_some()`。
#[derive(Deserialize)]
struct ReadResponse {
    content: Option<String>,
}

/// ListResponse 对应 `GET fs/list` 的响应体 `{files: [...]}`（相对路径、正斜杠、
/// 已排序；目录不存在时 files 为空数组，不是错误）。
#[derive(Deserialize)]
struct ListResponse {
    files: Vec<String>,
}

/// WriteResponse 对应 `PUT fs/write` 的响应体 `{backup_path}`；未备份时该字段
/// 是空字符串而不是缺省/null，需要显式转换成 `Option<String>`。
#[derive(Deserialize)]
struct WriteResponse {
    backup_path: String,
}

/// ErrorBody 是非 2xx 响应体的宽松解析形态。
///
/// 目标机各 handler 的错误体字段并不统一：`jsonCodeError` 产出 `{code,error}`
/// （403/502 等），write-batch 部分失败时的 `jsonWrite` 产出 `{error,written}`
/// （无 `code`）。这里只取跨端点共有、且用得上的两个字段；`code` 不需要解析出来
/// 参与判断——本模块按 HTTP 状态码本身分流（403/502/其它），状态码已经是稳定
/// 契约的一部分。
#[derive(Deserialize, Default)]
struct ErrorBody {
    error: Option<String>,
    written: Option<Vec<String>>,
}

/// extract_error_message 从非 2xx 响应体里取出对用户有意义的文案。
///
/// 能解析出 `error` 字段就用它（比裸 JSON 可读）；write-batch 部分失败时还会
/// 带上已成功写入的文件数，帮用户判断磁盘当前处于什么状态（对应 Go 端点两阶段
/// 写入设计里「已写入清单」的语义）。响应体不是预期 JSON 形态时原样回退到裸
/// 文本，不吞掉任何信息。
fn extract_error_message(response: ureq::Response) -> String {
    let body = response.into_string().unwrap_or_default();
    match serde_json::from_str::<ErrorBody>(&body) {
        Ok(parsed) => {
            let base = parsed.error.unwrap_or_else(|| body.clone());
            match parsed.written {
                Some(written) if !written.is_empty() => {
                    format!("{base}（已写入 {} 个文件）", written.len())
                }
                _ => base,
            }
        }
        Err(_) => body,
    }
}

/// wrap_write_failure 把 execute() 返回的通用错误串接上调用方给的 WriteLabels
/// 名词，让远端写失败的措辞与本地保持同一习惯（brief 明确要求：write_atomic
/// 的 labels 参数「远端实现要把它同样拼进错误串」）。
///
/// Go 端点把「备份失败」与「建目录/写入失败」统一映射成同一个 500，只能靠它
/// 固定的 error 文案（`"backup failed"`，见 handler_integrations_fs.go）区分是
/// 哪一步——这是对 Go 侧字符串常量的弱耦合，一旦 Go 改了这个词，判断会退化成
/// 走 write_object 分支（错误依然可读、依然带着对象名词，只是不够精确），
/// 不会 panic 或丢信息。
fn wrap_write_failure(cause: &str, labels: &WriteLabels<'_>) -> String {
    if cause.contains("backup failed") {
        format!("{}: {cause}", labels.backup_failure)
    } else {
        format!("写入{}失败: {cause}", labels.write_object)
    }
}

/// to_remote_rel_path 把 rel_path 转成远端 write-batch 端点要求的正斜杠形式。
///
/// 目标机可能是 Windows（agent 二进制会被编译到 Windows 远端机器上运行），Go 端
/// `integrationRelPathSafe` 显式拒绝反斜杠；这里统一做一次替换，不依赖调用方
/// 构造 rel_path 时用的是哪个平台的分隔符。
fn to_remote_rel_path(path: &Path) -> String {
    path.to_string_lossy().replace('\\', "/")
}

impl ConnectorFs for RemoteAgentFs {
    fn stat(&self, path: &Path) -> Result<FsStat, String> {
        tracing::debug!(host_id = %self.host_id, path = %path.display(), "remote fs stat");
        let path_str = path.to_string_lossy().into_owned();
        let raw = self.execute(
            "GET",
            "fs/stat",
            Some(("path", &path_str)),
            None,
            "stat",
            &path_str,
        )?;
        let parsed: StatResponse = serde_json::from_str(&raw).map_err(|error| {
            format!(
                "远端机器 {} stat 响应解析失败: {error}（path={path_str}）",
                self.host_id
            )
        })?;
        Ok(FsStat {
            exists: parsed.exists,
            is_dir: parsed.is_dir,
        })
    }

    fn read_optional(&self, path: &Path) -> Result<Option<String>, String> {
        tracing::debug!(host_id = %self.host_id, path = %path.display(), "remote fs read");
        let path_str = path.to_string_lossy().into_owned();
        let raw = self.execute(
            "GET",
            "fs/read",
            Some(("path", &path_str)),
            None,
            "read",
            &path_str,
        )?;
        let parsed: ReadResponse = serde_json::from_str(&raw).map_err(|error| {
            format!(
                "远端机器 {} read 响应解析失败: {error}（path={path_str}）",
                self.host_id
            )
        })?;
        // 判定存在性只看 content 是否出现，不看（也没有建模）exists 字段——
        // 见 ReadResponse 的文档注释。
        Ok(parsed.content)
    }

    fn write_atomic(
        &self,
        path: &Path,
        content: &str,
        backup: bool,
        labels: WriteLabels<'_>,
    ) -> Result<Option<String>, String> {
        tracing::info!(
            host_id = %self.host_id,
            path = %path.display(),
            bytes = content.len(),
            backup,
            "remote fs write"
        );
        #[derive(Serialize)]
        struct WriteRequest<'a> {
            path: String,
            content: &'a str,
            backup: bool,
        }
        let body = serde_json::to_string(&WriteRequest {
            path: path.to_string_lossy().into_owned(),
            content,
            backup,
        })
        .map_err(|error| format!("写入{}失败: 序列化请求失败: {error}", labels.write_object))?;

        let path_str = path.to_string_lossy().into_owned();
        let raw = self
            .execute("PUT", "fs/write", None, Some(body), "write", &path_str)
            .map_err(|cause| wrap_write_failure(&cause, &labels))?;

        let parsed: WriteResponse = serde_json::from_str(&raw).map_err(|error| {
            format!(
                "写入{}失败: 远端机器 {} 响应解析失败: {error}",
                labels.write_object, self.host_id
            )
        })?;
        Ok(if parsed.backup_path.is_empty() {
            None
        } else {
            Some(parsed.backup_path)
        })
    }

    fn mkdir_all(&self, path: &Path) -> Result<(), String> {
        // 受限文件端点没有独立的 mkdir 原语（Task 4 契约只有 stat/read/list/
        // write/rename/write-batch/delete 七条）。本仓库现有全部调用点
        // （install_to_path 系列的「创建配置目录」、install_skill_dir_core 的
        // 「创建唯一 skill 临时目录」）无一例外紧跟着一次 write_atomic 或
        // write_batch，而 Go 的 fs/write 与 fs/write-batch 端点都会在写入前
        // 自行 `os.MkdirAll(父目录)`——因此这里天然是 no-op，不发起任何网络
        // 请求。**不要**为了「忠于本地语义」而尝试拼一个假端点或复用 write
        // 端点去创建空目录：那既没有对应的 Go handler，也没有内容可白名单
        // 校验。若未来出现「mkdir_all 后不紧跟写入」的新调用点，这个假设会
        // 失效，需要那时候再补一个真正的远端 mkdir 原语。
        tracing::debug!(
            host_id = %self.host_id,
            path = %path.display(),
            "remote fs mkdir_all is a no-op (server-side write endpoints create parent dirs)"
        );
        Ok(())
    }

    fn write_batch(&self, dir: &Path, files: &[BatchFile], label: &str) -> Result<(), String> {
        let total_bytes: usize = files.iter().map(|file| file.content.len()).sum();
        tracing::info!(
            host_id = %self.host_id,
            dir = %dir.display(),
            files = files.len(),
            bytes = total_bytes,
            label,
            "remote fs write-batch"
        );
        #[derive(Serialize)]
        struct FileRequest {
            rel_path: String,
            content: String,
            executable: bool,
        }
        #[derive(Serialize)]
        struct WriteBatchRequest {
            dir: String,
            files: Vec<FileRequest>,
        }

        let mut request_files = Vec::with_capacity(files.len());
        for file in files {
            // Go 端点的 content 字段是 JSON 字符串（非 base64），只能承载合法
            // UTF-8 文本；skill/hook/配置文件都是文本，这里显式拒绝而不是静默
            // 用 from_utf8_lossy 替换非法字节篡改文件内容。
            let content = String::from_utf8(file.content.clone()).map_err(|_| {
                format!(
                    "写入 {label} 失败: {} 不是合法 UTF-8 文本，远端受限文件通道只接受文本内容",
                    file.rel_path.display()
                )
            })?;
            request_files.push(FileRequest {
                rel_path: to_remote_rel_path(&file.rel_path),
                content,
                executable: file.executable,
            });
        }

        let body = serde_json::to_string(&WriteBatchRequest {
            dir: dir.to_string_lossy().into_owned(),
            files: request_files,
        })
        .map_err(|error| format!("写入 {label} 失败: 序列化请求失败: {error}"))?;

        let dir_str = dir.to_string_lossy().into_owned();
        self.execute(
            "PUT",
            "fs/write-batch",
            None,
            Some(body),
            "write-batch",
            &dir_str,
        )
        .map(|_| ())
        .map_err(|cause| format!("写入 {label} 失败: {cause}"))
    }

    fn rename(&self, from: &Path, to: &Path) -> Result<(), String> {
        tracing::info!(
            host_id = %self.host_id,
            from = %from.display(),
            to = %to.display(),
            "remote fs rename"
        );
        #[derive(Serialize)]
        struct RenameRequest {
            from: String,
            to: String,
        }
        let body = serde_json::to_string(&RenameRequest {
            from: from.to_string_lossy().into_owned(),
            to: to.to_string_lossy().into_owned(),
        })
        .map_err(|error| format!("远端机器 {} rename 请求序列化失败: {error}", self.host_id))?;
        // rename 涉及两个独立路径，任一侧都可能被白名单拒绝；错误文案里两侧都带上，
        // 避免用户只看到一侧路径就误以为是另一侧的问题。
        let context = format!("{} -> {}", from.display(), to.display());
        self.execute("POST", "fs/rename", None, Some(body), "rename", &context)
            .map(|_| ())
    }

    fn list_relative_files(&self, dir: &Path) -> Result<Vec<PathBuf>, String> {
        tracing::debug!(host_id = %self.host_id, dir = %dir.display(), "remote fs list");
        let dir_str = dir.to_string_lossy().into_owned();
        let raw = self.execute(
            "GET",
            "fs/list",
            Some(("path", &dir_str)),
            None,
            "list",
            &dir_str,
        )?;
        let parsed: ListResponse = serde_json::from_str(&raw).map_err(|error| {
            format!(
                "远端机器 {} list 响应解析失败: {error}（dir={dir_str}）",
                self.host_id
            )
        })?;
        Ok(parsed.files.into_iter().map(PathBuf::from).collect())
    }

    fn remove_dir_all(&self, path: &Path) -> Result<(), String> {
        tracing::info!(host_id = %self.host_id, path = %path.display(), "remote fs delete");
        let path_str = path.to_string_lossy().into_owned();
        self.execute(
            "DELETE",
            "fs",
            Some(("path", &path_str)),
            None,
            "delete",
            &path_str,
        )
        .map(|_| ())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::io::{BufRead, BufReader, Read, Write};
    use std::net::{TcpListener, TcpStream};
    use std::sync::mpsc;
    use std::thread;

    /// RecordedRequest 是极简 fake agent 捕获到的单次请求快照，供测试断言
    /// 「真的发出了预期的方法/路径/请求头/body」。
    struct RecordedRequest {
        method: String,
        request_target: String,
        headers: HashMap<String, String>,
        body: String,
    }

    /// FakeResponse 描述 fake agent 要回给客户端的一次性响应。
    struct FakeResponse {
        status: u16,
        reason: &'static str,
        body: String,
    }

    fn ok(body: &str) -> FakeResponse {
        FakeResponse {
            status: 200,
            reason: "OK",
            body: body.to_string(),
        }
    }

    fn status(status: u16, reason: &'static str, body: &str) -> FakeResponse {
        FakeResponse {
            status,
            reason,
            body: body.to_string(),
        }
    }

    /// spawn_fake_agent 起一个只接受一次连接、读完整请求后按 responder 回一次
    /// 响应的极简 HTTP/1.1 服务，返回其 origin（"http://127.0.0.1:PORT"）与一个
    /// 接收 RecordedRequest 的 channel。
    ///
    /// 选它而不是「泛型化传输闭包」的原因：真的走一遍 HTTP 请求行/请求头/query
    /// 编码/JSON body 的序列化，再走一遍响应体的反序列化——闭包 fake 会绕开这整条
    /// 链路，抓不住字段名拼错、query 里 path 有没有被正确 percent-encode、
    /// `{exists:false}` 判定写反这类只会在真实往返里暴露的问题（brief 点名的
    /// `exists` 陷阱正是这一类）。
    fn spawn_fake_agent<F>(responder: F) -> (String, mpsc::Receiver<RecordedRequest>)
    where
        F: Fn(&RecordedRequest) -> FakeResponse + Send + 'static,
    {
        let listener = TcpListener::bind("127.0.0.1:0").expect("bind fake agent");
        let addr = listener.local_addr().expect("fake agent addr");
        let (tx, rx) = mpsc::channel();
        thread::spawn(move || {
            if let Ok((mut stream, _)) = listener.accept() {
                let request = read_request(&mut stream);
                let response = responder(&request);
                write_response(&mut stream, &response);
                let _ = tx.send(request);
            }
        });
        (format!("http://{addr}"), rx)
    }

    fn read_request(stream: &mut TcpStream) -> RecordedRequest {
        let mut reader = BufReader::new(stream.try_clone().expect("clone stream"));
        let mut request_line = String::new();
        reader
            .read_line(&mut request_line)
            .expect("read request line");
        let mut parts = request_line.split_whitespace();
        let method = parts.next().unwrap_or_default().to_string();
        let request_target = parts.next().unwrap_or_default().to_string();

        let mut headers = HashMap::new();
        loop {
            let mut line = String::new();
            reader.read_line(&mut line).expect("read header line");
            let trimmed = line.trim_end_matches(['\r', '\n']);
            if trimmed.is_empty() {
                break;
            }
            if let Some((key, value)) = trimmed.split_once(':') {
                headers.insert(key.trim().to_ascii_lowercase(), value.trim().to_string());
            }
        }
        let content_length: usize = headers
            .get("content-length")
            .and_then(|value| value.parse().ok())
            .unwrap_or(0);
        let mut body = vec![0u8; content_length];
        if content_length > 0 {
            reader.read_exact(&mut body).expect("read body");
        }
        RecordedRequest {
            method,
            request_target,
            headers,
            body: String::from_utf8_lossy(&body).into_owned(),
        }
    }

    fn write_response(stream: &mut TcpStream, response: &FakeResponse) {
        let payload = format!(
            "HTTP/1.1 {} {}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
            response.status,
            response.reason,
            response.body.len(),
            response.body
        );
        let _ = stream.write_all(payload.as_bytes());
        let _ = stream.flush();
    }

    /// decode_query_value 是测试专用的极简 percent-decode，用来验证 ureq 的
    /// `.query()` 确实把 path 里的空格/中文正确编码进了请求行（而不是断言编码后
    /// 的字节串本身，那样测试会跟编码实现细节耦合）。
    fn decode_query_value(request_target: &str, key: &str) -> String {
        let query = request_target.split_once('?').map(|(_, q)| q).unwrap_or("");
        let raw = query
            .split('&')
            .find_map(|pair| pair.strip_prefix(&format!("{key}=")))
            .unwrap_or("");
        let mut bytes = Vec::new();
        let mut chars = raw.chars().peekable();
        while let Some(c) = chars.next() {
            match c {
                '+' => bytes.push(b' '),
                '%' => {
                    let hi = chars.next().unwrap_or('0');
                    let lo = chars.next().unwrap_or('0');
                    let byte = u8::from_str_radix(&format!("{hi}{lo}"), 16).unwrap_or(b'?');
                    bytes.push(byte);
                }
                other => {
                    let mut buf = [0u8; 4];
                    bytes.extend_from_slice(other.encode_utf8(&mut buf).as_bytes());
                }
            }
        }
        String::from_utf8_lossy(&bytes).into_owned()
    }

    fn fs(base: String) -> RemoteAgentFs {
        RemoteAgentFs::new(base, "test-local-token".to_string(), "h1".to_string())
    }

    fn config_labels() -> WriteLabels<'static> {
        WriteLabels {
            write_object: "配置文件",
            backup_failure: "备份配置文件失败",
        }
    }

    // ---- stat ----

    #[test]
    fn stat_parses_exists_and_is_dir_and_sends_authenticated_get() {
        let (base, rx) = spawn_fake_agent(|_req| ok(r#"{"exists":true,"is_dir":true,"size":12}"#));
        let result = fs(base)
            .stat(Path::new("/home/x/.claude.json"))
            .expect("stat succeeds");
        assert!(result.exists);
        assert!(result.is_dir);

        let request = rx.recv().expect("request recorded");
        assert_eq!(request.method, "GET");
        assert!(
            request
                .request_target
                .starts_with("/api/agents/h1/integrations/fs/stat?path="),
            "unexpected target: {}",
            request.request_target
        );
        assert_eq!(
            request.headers.get("authorization").map(String::as_str),
            Some("Bearer test-local-token")
        );
    }

    #[test]
    fn stat_encodes_path_with_space_and_unicode_correctly() {
        let (base, rx) = spawn_fake_agent(|_req| ok(r#"{"exists":false,"is_dir":false}"#));
        let target_path = Path::new("/home/x/.claude 目录/settings.json");
        fs(base).stat(target_path).expect("stat succeeds");

        let request = rx.recv().expect("request recorded");
        let decoded = decode_query_value(&request.request_target, "path");
        assert_eq!(decoded, target_path.to_string_lossy());
    }

    // ---- read_optional：{exists:false} → Ok(None) ----

    #[test]
    fn read_optional_returns_none_when_target_missing() {
        let (base, _rx) = spawn_fake_agent(|_req| ok(r#"{"exists":false}"#));
        let result = fs(base)
            .read_optional(Path::new("/home/x/.claude.json"))
            .expect("read succeeds");
        assert_eq!(result, None);
    }

    #[test]
    fn read_optional_returns_content_when_present() {
        let (base, _rx) = spawn_fake_agent(|_req| ok(r#"{"content":"hello"}"#));
        let result = fs(base)
            .read_optional(Path::new("/home/x/.claude.json"))
            .expect("read succeeds");
        assert_eq!(result, Some("hello".to_string()));
    }

    // ---- write_atomic：请求形状 + backup_path 解析 ----

    #[test]
    fn write_atomic_sends_expected_request_and_returns_backup_path() {
        let (base, rx) = spawn_fake_agent(|_req| ok(r#"{"backup_path":"/x.json.superdev-bak"}"#));
        let backup = fs(base)
            .write_atomic(
                Path::new("/home/x/.claude.json"),
                "{}\n",
                true,
                config_labels(),
            )
            .expect("write succeeds");
        assert_eq!(backup, Some("/x.json.superdev-bak".to_string()));

        let request = rx.recv().expect("request recorded");
        assert_eq!(request.method, "PUT");
        assert_eq!(
            request.request_target,
            "/api/agents/h1/integrations/fs/write"
        );
        let body: serde_json::Value = serde_json::from_str(&request.body).expect("valid json");
        assert_eq!(body["path"], "/home/x/.claude.json");
        assert_eq!(body["content"], "{}\n");
        assert_eq!(body["backup"], true);
    }

    #[test]
    fn write_atomic_treats_empty_backup_path_as_none() {
        let (base, _rx) = spawn_fake_agent(|_req| ok(r#"{"backup_path":""}"#));
        let backup = fs(base)
            .write_atomic(
                Path::new("/home/x/fresh.json"),
                "{}\n",
                true,
                config_labels(),
            )
            .expect("write succeeds");
        assert_eq!(backup, None);
    }

    #[test]
    fn write_atomic_generic_failure_uses_write_object_label() {
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(500, "Internal Server Error", r#"{"error":"write failed"}"#)
        });
        let error = fs(base)
            .write_atomic(
                Path::new("/home/x/.claude.json"),
                "{}\n",
                true,
                config_labels(),
            )
            .expect_err("write must fail");
        assert!(
            error.starts_with("写入配置文件失败: "),
            "expected write_object label prefix: {error}"
        );
    }

    #[test]
    fn write_atomic_backup_failure_uses_backup_label() {
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(500, "Internal Server Error", r#"{"error":"backup failed"}"#)
        });
        let error = fs(base)
            .write_atomic(
                Path::new("/home/x/.claude.json"),
                "{}\n",
                true,
                config_labels(),
            )
            .expect_err("write must fail");
        assert!(
            error.starts_with("备份配置文件失败: "),
            "expected backup_failure label prefix: {error}"
        );
    }

    // ---- 403 path_not_allowed → 带路径的中文错误串 ----

    #[test]
    fn forbidden_path_maps_to_chinese_error_with_path() {
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(
                403,
                "Forbidden",
                r#"{"code":"path_not_allowed","error":"path not allowed"}"#,
            )
        });
        let error = fs(base)
            .stat(Path::new("/etc/passwd"))
            .expect_err("stat must be forbidden");
        assert!(error.contains("h1"), "error should name host_id: {error}");
        assert!(
            error.contains("/etc/passwd"),
            "error should include the offending path: {error}"
        );
        assert!(
            error.contains("不允许") || error.contains("拒绝"),
            "error should read as a permission denial in Chinese: {error}"
        );
    }

    #[test]
    fn rename_forbidden_path_names_both_sides() {
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(
                403,
                "Forbidden",
                r#"{"code":"path_not_allowed","error":"path not allowed"}"#,
            )
        });
        let error = fs(base)
            .rename(Path::new("/home/x/a"), Path::new("/etc/b"))
            .expect_err("rename must be forbidden");
        assert!(error.contains("/home/x/a"), "missing from side: {error}");
        assert!(error.contains("/etc/b"), "missing to side: {error}");
    }

    // ---- 网络错误 → 「远端机器不可达」 ----

    #[test]
    fn connection_refused_maps_to_unreachable_message() {
        // 不启动任何 fake agent：指向一个在本机几乎不可能被监听的端口，
        // 触发 ureq 的传输层错误（连接被拒绝），而不是业务层的非 2xx 响应。
        let unreachable = fs("http://127.0.0.1:1".to_string());
        let error = unreachable
            .stat(Path::new("/home/x/.claude.json"))
            .expect_err("connection must fail");
        assert!(
            error.contains("远端机器") && error.contains("不可达"),
            "expected unreachable-style message: {error}"
        );
        assert!(error.contains("h1"), "error should name host_id: {error}");
    }

    #[test]
    fn bad_gateway_target_unreachable_maps_to_unreachable_message() {
        // 502 integration_target_unreachable：本机 agent 本身可达，但它转发给
        // 目标机时失败——同样必须落在「远端机器不可达」这一类错误文案。
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(
                502,
                "Bad Gateway",
                r#"{"code":"integration_target_unreachable","error":"dial tcp: connection refused"}"#,
            )
        });
        let error = fs(base)
            .stat(Path::new("/home/x/.claude.json"))
            .expect_err("stat must fail");
        assert!(
            error.contains("远端机器") && error.contains("不可达"),
            "expected unreachable-style message: {error}"
        );
        assert!(
            error.contains("dial tcp"),
            "underlying transport error text should be preserved: {error}"
        );
    }

    // ---- 其它非 2xx → 带 host_id 与操作名上下文 ----

    #[test]
    fn unknown_host_404_includes_host_id_and_operation() {
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(404, "Not Found", r#"{"error":"agent not configured"}"#)
        });
        let error = fs(base)
            .stat(Path::new("/home/x/.claude.json"))
            .expect_err("stat must fail");
        assert!(error.contains("h1"), "error should name host_id: {error}");
        assert!(
            error.contains("stat"),
            "error should name operation: {error}"
        );
        assert!(
            error.contains("404"),
            "error should include status: {error}"
        );
        assert!(
            error.contains("agent not configured"),
            "error should preserve underlying message: {error}"
        );
    }

    // ---- list / rename / delete：请求形状 ----

    #[test]
    fn list_relative_files_parses_files_array() {
        let (base, rx) =
            spawn_fake_agent(|_req| ok(r#"{"files":["SKILL.md","references/log-tools.md"]}"#));
        let files = fs(base)
            .list_relative_files(Path::new("/home/x/.claude/skills/superdev"))
            .expect("list succeeds");
        assert_eq!(
            files,
            vec![
                PathBuf::from("SKILL.md"),
                PathBuf::from("references/log-tools.md"),
            ]
        );
        let request = rx.recv().expect("request recorded");
        assert_eq!(request.method, "GET");
        assert!(request
            .request_target
            .starts_with("/api/agents/h1/integrations/fs/list?path="));
    }

    #[test]
    fn list_relative_files_treats_missing_dir_as_empty() {
        let (base, _rx) = spawn_fake_agent(|_req| ok(r#"{"files":[]}"#));
        let files = fs(base)
            .list_relative_files(Path::new("/home/x/.claude/skills/superdev"))
            .expect("list succeeds");
        assert!(files.is_empty());
    }

    #[test]
    fn rename_sends_from_and_to_in_body() {
        let (base, rx) = spawn_fake_agent(|_req| ok("{}"));
        fs(base)
            .rename(
                Path::new("/home/x/skills/superdev"),
                Path::new("/home/x/skills/superdev.superdev-bak"),
            )
            .expect("rename succeeds");

        let request = rx.recv().expect("request recorded");
        assert_eq!(request.method, "POST");
        assert_eq!(
            request.request_target,
            "/api/agents/h1/integrations/fs/rename"
        );
        let body: serde_json::Value = serde_json::from_str(&request.body).expect("valid json");
        assert_eq!(body["from"], "/home/x/skills/superdev");
        assert_eq!(body["to"], "/home/x/skills/superdev.superdev-bak");
    }

    #[test]
    fn remove_dir_all_sends_delete_with_path_query() {
        let (base, rx) = spawn_fake_agent(|_req| ok("{}"));
        fs(base)
            .remove_dir_all(Path::new("/home/x/skills/superdev.tmp-123"))
            .expect("delete succeeds");

        let request = rx.recv().expect("request recorded");
        assert_eq!(request.method, "DELETE");
        assert!(request
            .request_target
            .starts_with("/api/agents/h1/integrations/fs?path="));
    }

    // ---- write_batch ----

    #[test]
    fn write_batch_sends_files_with_forward_slash_rel_paths() {
        let (base, rx) =
            spawn_fake_agent(|_req| ok(r#"{"written":["SKILL.md","hooks/run-hook.cmd"]}"#));
        let files = vec![
            BatchFile {
                rel_path: PathBuf::from("SKILL.md"),
                content: b"# SuperDev\n".to_vec(),
                executable: false,
            },
            BatchFile {
                rel_path: PathBuf::from("hooks").join("run-hook.cmd"),
                content: b"#!/bin/sh\n".to_vec(),
                executable: true,
            },
        ];
        fs(base)
            .write_batch(
                Path::new("/home/x/.claude/skills/superdev"),
                &files,
                "skill 文件",
            )
            .expect("write batch succeeds");

        let request = rx.recv().expect("request recorded");
        assert_eq!(request.method, "PUT");
        let body: serde_json::Value = serde_json::from_str(&request.body).expect("valid json");
        assert_eq!(body["dir"], "/home/x/.claude/skills/superdev");
        assert_eq!(body["files"][0]["rel_path"], "SKILL.md");
        assert_eq!(body["files"][1]["rel_path"], "hooks/run-hook.cmd");
        assert_eq!(body["files"][1]["executable"], true);
    }

    #[test]
    fn write_batch_partial_failure_names_the_label_and_written_count() {
        let (base, _rx) = spawn_fake_agent(|_req| {
            status(
                500,
                "Internal Server Error",
                r#"{"error":"write failed","written":["SKILL.md"]}"#,
            )
        });
        let files = vec![
            BatchFile {
                rel_path: PathBuf::from("SKILL.md"),
                content: b"# SuperDev\n".to_vec(),
                executable: false,
            },
            BatchFile {
                rel_path: PathBuf::from("references").join("log-tools.md"),
                content: b"docs\n".to_vec(),
                executable: false,
            },
        ];
        let error = fs(base)
            .write_batch(
                Path::new("/home/x/.claude/skills/superdev"),
                &files,
                "skill 文件",
            )
            .expect_err("write batch must fail");
        assert!(error.contains("skill 文件"), "missing label: {error}");
        assert!(
            error.contains("已写入 1 个文件"),
            "missing partial progress info: {error}"
        );
    }

    #[test]
    fn write_batch_rejects_non_utf8_content_without_any_network_call() {
        // 指向不可达地址，证明校验在发起请求之前就已经失败——不是网络错误
        // 被误判成了「内容非法」。
        let unreachable = fs("http://127.0.0.1:1".to_string());
        let files = vec![BatchFile {
            rel_path: PathBuf::from("binary.dat"),
            content: vec![0xff, 0xfe, 0x00],
            executable: false,
        }];
        let error = unreachable
            .write_batch(Path::new("/home/x/skills/superdev"), &files, "skill 文件")
            .expect_err("non-utf8 content must be rejected");
        assert!(error.contains("binary.dat"));
        assert!(error.contains("UTF-8"));
        assert!(
            !error.contains("不可达"),
            "must fail before any network attempt: {error}"
        );
    }

    // ---- mkdir_all：no-op，不发网络请求 ----

    #[test]
    fn mkdir_all_is_a_no_op_and_never_touches_the_network() {
        // 指向不可达地址：如果 mkdir_all 真的发起了网络请求，这里会返回
        // 「远端机器不可达」错误；no-op 实现应该无条件 Ok(())。
        let unreachable = fs("http://127.0.0.1:1".to_string());
        unreachable
            .mkdir_all(Path::new("/home/x/.claude/skills/superdev.tmp-1"))
            .expect("mkdir_all must be Ok without any network call");
    }

    // ---- cli_commands：8 家连接器均非空 ----

    #[test]
    fn all_built_in_connectors_report_non_empty_cli_commands() {
        // connectors::builtin() 是既有的、生产 Registry 初始化就在用的「八个内置
        // 连接器」工厂——直接复用它遍历，而不是自己在测试里重新拼一遍 8 个
        // 构造调用，避免测试覆盖的集合和生产注册的集合悄悄脱节。
        let connectors = super::super::connectors::builtin();
        assert_eq!(
            connectors.len(),
            8,
            "expected all eight built-in connectors"
        );
        for connector in &connectors {
            let commands = connector.cli_commands();
            assert!(
                !commands.is_empty(),
                "connector {} must report at least one cli command",
                connector.descriptor().id()
            );
        }
    }
}
