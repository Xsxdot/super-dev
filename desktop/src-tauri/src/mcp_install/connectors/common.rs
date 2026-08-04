// common.rs 提供第二波 Agent Connector 的私有描述符与安全配置/Skill 原语。
//
// 职责：
//   - 构造标准/完整支持级别的内置描述符
//   - 以固定安全顺序执行无损配置突变（校验目标 → 变换 → 备份 → 原子写）
//   - 封装 Skill 状态查询与安装/卸载结果映射
//
// 边界：
//   - 不解析任何 Agent 方言（JSONC/YAML/CLI）
//   - 不读取环境变量、不启动外部进程
//   - 不向日志写入配置路径或文件内容

use crate::mcp_install::contracts::*;
#[cfg(test)]
use crate::mcp_install::fs_port::LocalFs;
use crate::mcp_install::fs_port::{ConnectorFs, WritePolicy, WriteTarget};
use crate::mcp_install::registry::*;
// 一律用带 `_with_fs` / `_from_source` 的端口版本并显式传端口：不带后缀的同名
// 函数只对测试可见，生产代码里「忘记传端口」会是编译错误而不是静默写到本机。
use crate::mcp_install::{
    install_skill_dir_from_source, remove_skill_dir_with_fs, skill_status_for_target, McpEntry,
    MergeResult, SkillInstallOutcome, CONFIG_WRITE_LABELS,
};
#[cfg(test)]
use std::fs;
use std::path::Path;
#[cfg(test)]
use std::path::PathBuf;
use std::time::Instant;

/// FileMutationOutcome 描述一次安全配置写入的结果摘要。
///
/// 仅包含变更标记与可选备份路径，不包含文件内容。
#[derive(Clone, Debug, PartialEq, Eq)]
pub(super) struct FileMutationOutcome {
    /// changed 表示磁盘内容是否实际发生写入。
    pub changed: bool,
    /// backup_path 是写入前生成的备份路径（仅当原文件存在且发生写入时）。
    pub backup_path: Option<String>,
}

/// descriptor 构造第二波内置连接器的标准描述符。
///
/// 参数：
///   - id: 开放字符串连接器 ID
///   - display_name: 展示名称
///   - hook_support: Session Hook 的支持方式；MCP 与 Skill 固定为 Automatic
///   - docs_url: 可选文档链接
///
/// 返回：
///   - 已通过契约校验的内置描述符
///
/// 注意：
///   - Hook 为 Automatic 时 support_level 派生为 Full，否则为 Standard
pub(super) fn descriptor(
    id: &str,
    display_name: &str,
    hook_support: SupportMode,
    docs_url: Option<&str>,
) -> AgentConnectorDescriptor {
    AgentConnectorDescriptor::new(AgentConnectorDescriptorInput {
        id: id.into(),
        display_name: display_name.into(),
        built_in: true,
        platforms: vec![
            ConnectorPlatform::Macos,
            ConnectorPlatform::Windows,
            ConnectorPlatform::Linux,
        ],
        integrations: vec![
            IntegrationSupport {
                capability: IntegrationCapability::Mcp,
                support: SupportMode::Automatic,
            },
            IntegrationSupport {
                capability: IntegrationCapability::Skill,
                support: SupportMode::Automatic,
            },
            IntegrationSupport {
                capability: IntegrationCapability::SessionHook,
                support: hook_support,
            },
        ],
        operations: [
            ConnectorOperation::Detect,
            ConnectorOperation::Install,
            ConnectorOperation::Update,
            ConnectorOperation::Status,
            ConnectorOperation::Uninstall,
            ConnectorOperation::Verify,
        ]
        .into_iter()
        .map(|operation| OperationSupport {
            operation,
            support: SupportMode::Automatic,
        })
        .collect(),
        docs_url: docs_url.map(str::to_string),
        verified_versions: None,
    })
    .expect("verified connector descriptor")
}

/// entry 从运行上下文构造 SuperDev MCP 入口。
///
/// 参数：
///   - ctx: 已解析的连接器运行上下文
///
/// 返回：
///   - 由 `ctx.mcp_launch()` 派生的条目；本机场景下是 MCP 二进制路径 + 空 args +
///     默认 Agent URL，远端场景（Task 9）会带上 `["mcp"]` 与目标机 Agent URL
pub(super) fn entry(ctx: &ConnectorRuntimeContext) -> McpEntry {
    McpEntry::from_launch(ctx.mcp_launch())
}

/// extract_json_mcp_runtime 从标准 `mcpServers.superdev` JSON 片段读取运行时命令与 Agent URL。
///
/// 参数：
///   - root: 已解析的 JSON 根对象
///
/// 返回：
///   - (mcp_command, agent_url)；字段缺失或为空时对应为 None
///
/// 注意：
///   - command 支持字符串与首元素为字符串的数组两种 schema
pub(super) fn extract_json_mcp_runtime(
    root: &serde_json::Value,
) -> (Option<String>, Option<String>) {
    let Some(server) = root
        .get("mcpServers")
        .and_then(|servers| servers.get("superdev"))
    else {
        return (None, None);
    };
    let command = match server.get("command") {
        Some(serde_json::Value::String(value)) if !value.trim().is_empty() => Some(value.clone()),
        Some(serde_json::Value::Array(items)) => items
            .first()
            .and_then(|item| item.as_str())
            .filter(|value| !value.trim().is_empty())
            .map(str::to_string),
        _ => None,
    };
    let agent_url = server
        .get("env")
        .and_then(|env| env.get("SUPERDEV_AGENT_URL"))
        .and_then(|value| value.as_str())
        .filter(|value| !value.trim().is_empty())
        .map(str::to_string);
    (command, agent_url)
}

/// integration_result 构造单项集成操作结果。
///
/// 参数：
///   - capability / result / target_path / backup_path / message: 结果字段
///
/// 返回：
///   - 填充完毕的 IntegrationOperationResult
pub(super) fn integration_result(
    capability: IntegrationCapability,
    result: IntegrationResult,
    target_path: Option<String>,
    backup_path: Option<String>,
    message: Option<String>,
) -> IntegrationOperationResult {
    IntegrationOperationResult {
        capability,
        result,
        target_path,
        backup_path,
        message,
    }
}

/// manual_hook_result 返回需要用户手动完成 Session Hook 的结果。
///
/// 参数：
///   - target_path: 可选的 hook 配置路径提示（仅返回给调用方，不记日志）
///
/// 返回：
///   - NeedsAction 结果，message 非空以满足契约
pub(super) fn manual_hook_result(target_path: Option<String>) -> IntegrationOperationResult {
    integration_result(
        IntegrationCapability::SessionHook,
        IntegrationResult::NeedsAction,
        target_path,
        None,
        Some("Session Hook 需按连接器指引手动配置后才会生效".into()),
    )
}

/// skill_status 读取目标 Skill 目录相对 bundled 源的状态。
///
/// 参数：
///   - fs_port: **目标机**文件操作端口（本机装传 LocalFs，远端装传 RemoteAgentFs）
///   - ctx: 运行上下文（含 skill 源）
///   - skill_path: 目标 Skill 目录
///
/// 返回：
///   - Skill 集成状态；路径仅出现在返回值中
///
/// 注意：
///   - skill **源**恒在桌面端本机（随桌面端打包），只有目标目录在 fs_port 那一侧；
///     `skill_status_for_target` 内部已经区分了这两者，本函数不需要额外处理
pub(super) fn skill_status(
    fs_port: &dyn ConnectorFs,
    ctx: &ConnectorRuntimeContext,
    skill_path: &Path,
) -> IntegrationState {
    let (installed, matches, error) = skill_status_for_target(
        fs_port,
        ctx.skill_source(),
        ctx.skill_source_error().map(str::to_string),
        skill_path,
    );
    let status = if let Some(err) = error.as_ref() {
        let _ = err;
        if installed {
            IntegrationStateStatus::Error
        } else {
            IntegrationStateStatus::Missing
        }
    } else if !installed {
        IntegrationStateStatus::Missing
    } else if matches == Some(true) {
        IntegrationStateStatus::Configured
    } else if matches == Some(false) {
        IntegrationStateStatus::NeedsAction
    } else {
        IntegrationStateStatus::Unknown
    };
    IntegrationState {
        capability: IntegrationCapability::Skill,
        status,
        target_path: Some(skill_path.to_string_lossy().into_owned()),
        message: error,
    }
}

/// install_skill 将 bundled SuperDev Skill 安装到目标目录。
///
/// 参数：
///   - fs_port: **目标机**文件操作端口
///   - ctx: 运行上下文
///   - skill_path: 目标 Skill 目录
///
/// 返回：
///   - 映射后的集成操作结果；源不可用时返回 Failed
pub(super) fn install_skill(
    fs_port: &dyn ConnectorFs,
    ctx: &ConnectorRuntimeContext,
    skill_path: &Path,
) -> IntegrationOperationResult {
    let target = skill_path.to_string_lossy().into_owned();
    match ctx.skill_source() {
        Some(source) => match install_skill_dir_from_source(fs_port, source, skill_path) {
            Ok(outcome) => skill_outcome_to_result(outcome),
            Err(error) => integration_result(
                IntegrationCapability::Skill,
                IntegrationResult::Failed,
                Some(target),
                None,
                Some(error),
            ),
        },
        None => integration_result(
            IntegrationCapability::Skill,
            IntegrationResult::Failed,
            Some(target),
            None,
            ctx.skill_source_error().map(str::to_string),
        ),
    }
}

/// uninstall_skill 删除目标 Skill 目录（幂等）。
///
/// 参数：
///   - fs_port: **目标机**文件操作端口
///   - skill_path: 目标 Skill 目录
///
/// 返回：
///   - 已删除时为 Installed（表示卸载变更），不存在时为 AlreadyPresent 的语义用 Skipped
pub(super) fn uninstall_skill(
    fs_port: &dyn ConnectorFs,
    skill_path: &Path,
) -> IntegrationOperationResult {
    let target = skill_path.to_string_lossy().into_owned();
    match remove_skill_dir_with_fs(fs_port, skill_path) {
        Ok(true) => integration_result(
            IntegrationCapability::Skill,
            IntegrationResult::Installed,
            Some(target),
            None,
            None,
        ),
        Ok(false) => integration_result(
            IntegrationCapability::Skill,
            IntegrationResult::AlreadyPresent,
            Some(target),
            None,
            None,
        ),
        Err(error) => integration_result(
            IntegrationCapability::Skill,
            IntegrationResult::Failed,
            Some(target),
            None,
            Some(error),
        ),
    }
}

fn skill_outcome_to_result(outcome: SkillInstallOutcome) -> IntegrationOperationResult {
    let result = if outcome.installed {
        IntegrationResult::Installed
    } else if outcome.already_present {
        IntegrationResult::AlreadyPresent
    } else {
        IntegrationResult::Failed
    };
    integration_result(
        IntegrationCapability::Skill,
        result,
        Some(outcome.target_path),
        outcome.backup_path,
        outcome.error,
    )
}

/// mutate_config 是 `mutate_config_with_fs(&LocalFs, ..)` 的旧签名封装。
///
/// **仅测试可见**：生产代码一律显式传端口，避免远端安装误调本地绑定版本而静默
/// 改到桌面机自己的磁盘；保留它只是为了让既有测试零改动。
#[cfg(test)]
pub(super) fn mutate_config<F>(
    connector_id: &str,
    path: &Path,
    transform: F,
) -> Result<FileMutationOutcome, ConnectorError>
where
    F: FnOnce(Option<&str>) -> Result<MergeResult, ConnectorError>,
{
    mutate_config_with_fs(&LocalFs, connector_id, path, transform)
}

/// mutate_config_with_fs 以固定安全顺序对配置文件执行格式相关变换。
///
/// 参数：
///   - fs_port: **目标机**文件操作端口（本机装传 LocalFs，远端装传 RemoteAgentFs）
///   - connector_id: 连接器 ID（仅用于结构化日志）
///   - path: 配置目标路径
///   - transform: 读取到的 UTF-8 文本（不存在时为空）到 MergeResult 的纯函数
///
/// 返回：
///   - 成功时的变更摘要；目标不安全或 I/O/变换失败时返回稳定错误码
///
/// 安全顺序：
///   1. 拒绝符号链接与非普通文件
///   2. 读取现有 UTF-8 或 NotFound 视为空
///   3. 先执行 transform，再考虑备份
///   4. changed=false 时直接返回，不写盘
///   5. 创建父目录 → 备份 → 原子写
pub(super) fn mutate_config_with_fs<F>(
    fs_port: &dyn ConnectorFs,
    connector_id: &str,
    path: &Path,
    transform: F,
) -> Result<FileMutationOutcome, ConnectorError>
where
    F: FnOnce(Option<&str>) -> Result<MergeResult, ConnectorError>,
{
    let started = Instant::now();
    tracing::debug!(
        connector_id,
        operation = "mutate_config",
        "connector config mutation started"
    );

    let result = mutate_config_inner(fs_port, path, transform);
    match &result {
        Ok(outcome) => tracing::info!(
            connector_id,
            operation = "mutate_config",
            changed = outcome.changed,
            duration_ms = started.elapsed().as_millis() as u64,
            "connector config mutation finished"
        ),
        Err(error) => tracing::error!(
            connector_id,
            operation = "mutate_config",
            error_code = error.code(),
            duration_ms = started.elapsed().as_millis() as u64,
            "connector config mutation failed"
        ),
    }
    result
}

fn mutate_config_inner<F>(
    fs_port: &dyn ConnectorFs,
    path: &Path,
    transform: F,
) -> Result<FileMutationOutcome, ConnectorError>
where
    F: FnOnce(Option<&str>) -> Result<MergeResult, ConnectorError>,
{
    // 1. 拒绝符号链接与非普通文件；不存在的目标允许创建。
    //    这里的 check 只负责**把失败提前**并给出与本机一致的错误码——真正的
    //    拒绝由第 7 步 `WritePolicy::CONFIG_FILE` 在写入端执行，因为 check 与
    //    write 之间隔着 TOCTOU 窗口（详见 WritePolicy::require_regular_file）。
    match fs_port.check_write_target(path) {
        Ok(WriteTarget::Absent) | Ok(WriteTarget::RegularFile) => {}
        Ok(WriteTarget::Unsafe) => {
            return Err(ConnectorError::new(
                "unsafe_config_target",
                "配置目标不是可安全写入的普通文件",
            ));
        }
        Err(error) => {
            return Err(ConnectorError::new(
                "config_stat_failed",
                format!("检查配置目标失败: {error}"),
            ));
        }
    }

    // 2. 读取 UTF-8 或将 NotFound 视为空输入。
    let existing = match fs_port.read_optional(path) {
        Ok(content) => content,
        Err(error) => {
            return Err(ConnectorError::new(
                "config_read_failed",
                format!("读取配置文件失败: {error}"),
            ));
        }
    };

    // 3. 先变换，确保非法内容不会触发备份或写入。
    let merged = transform(existing.as_deref())?;

    // 4. 无变更则直接返回。
    if !merged.changed {
        return Ok(FileMutationOutcome {
            changed: false,
            backup_path: None,
        });
    }

    // 5. 创建父目录。
    if let Some(parent) = path.parent() {
        fs_port.mkdir_all(parent).map_err(|error| {
            ConnectorError::new("config_parent_failed", format!("创建配置目录失败: {error}"))
        })?;
    }

    // 6+7. 备份（仅在原文件存在时）+ 原子写入，由端口一次完成。
    //
    // 端口把这两步合成了一次调用，只回一个错误串，因此「是备份失败还是写入
    // 失败」只能靠 labels 给的前缀分辨——这与 remote_fs.rs `wrap_write_failure`
    // 用的是同一手法，且两侧的判据串都是本仓库自己的常量。分辨结果只影响
    // ConnectorError 的 code（两个 code 都是既有的、调用方在用的），文案本身
    // 无论走哪个分支都与端口化之前逐字节相同。
    let backup = fs_port
        .write_atomic_with_policy(
            path,
            &merged.content,
            true,
            CONFIG_WRITE_LABELS,
            WritePolicy::CONFIG_FILE,
        )
        .map_err(|error| {
            if error.starts_with(CONFIG_WRITE_LABELS.backup_failure) {
                ConnectorError::new("config_backup_failed", error)
            } else {
                ConnectorError::new("config_write_failed", error)
            }
        })?;

    Ok(FileMutationOutcome {
        changed: true,
        backup_path: backup,
    })
}

/// remove_config_with_fs 通过变换函数删除配置中的托管条目。
///
/// 参数与返回语义同 [`mutate_config_with_fs`]；transform 负责生成删除后的文本。
///
/// 没有同名旧签名封装：它没有按旧签名调用的既有测试，直接加端口首参即可
/// （与 Task 6 对 `read_config_status` 的处理同一判据）。
pub(super) fn remove_config_with_fs<F>(
    fs_port: &dyn ConnectorFs,
    connector_id: &str,
    path: &Path,
    transform: F,
) -> Result<FileMutationOutcome, ConnectorError>
where
    F: FnOnce(Option<&str>) -> Result<MergeResult, ConnectorError>,
{
    let started = Instant::now();
    tracing::debug!(
        connector_id,
        operation = "remove_config",
        "connector config removal started"
    );
    let result = mutate_config_inner(fs_port, path, transform);
    match &result {
        Ok(outcome) => tracing::info!(
            connector_id,
            operation = "remove_config",
            changed = outcome.changed,
            duration_ms = started.elapsed().as_millis() as u64,
            "connector config removal finished"
        ),
        Err(error) => tracing::error!(
            connector_id,
            operation = "remove_config",
            error_code = error.code(),
            duration_ms = started.elapsed().as_millis() as u64,
            "connector config removal failed"
        ),
    }
    result
}

/// path_string 将路径转为返回值用的拥有字符串。
pub(super) fn path_string(path: &Path) -> String {
    path.to_string_lossy().into_owned()
}

/// test_dir 生成隔离的临时测试目录（避免线程名中的 `::`）。
#[cfg(test)]
pub(super) fn test_dir(label: &str) -> PathBuf {
    let root = std::env::temp_dir().join(format!(
        "connector-common-{}-{}-{}",
        label,
        std::process::id(),
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_nanos())
            .unwrap_or(0)
    ));
    let _ = fs::remove_dir_all(&root);
    fs::create_dir_all(&root).expect("create test dir");
    root
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn descriptor_level_is_derived_from_hook_support() {
        assert_eq!(
            descriptor("standard", "Standard", SupportMode::Manual, None).support_level(),
            Some(SupportLevel::Standard)
        );
        assert_eq!(
            descriptor("full", "Full", SupportMode::Automatic, None).support_level(),
            Some(SupportLevel::Full)
        );
    }

    #[test]
    fn mutate_config_rejects_a_directory_target_without_writing() {
        let root = test_dir("directory-target");
        let error = mutate_config("fixture", &root, |_| {
            Ok(MergeResult {
                content: "{}\n".into(),
                changed: true,
            })
        })
        .expect_err("directory must fail");
        assert_eq!(error.code(), "unsafe_config_target");
        let _ = fs::remove_dir_all(root);
    }

    /// 符号链接目标必须与端口化之前一样被拒（错误码 + 文案都不变）。
    ///
    /// 端口化之前这条守卫是 `fs::symlink_metadata` 直判；现在走
    /// `ConnectorFs::check_write_target`，判据换了位置，行为不能换。
    #[cfg(unix)]
    #[test]
    fn mutate_config_rejects_a_symlink_target_without_writing() {
        let root = test_dir("symlink-target");
        let real = root.join("real.json");
        fs::write(&real, "{}\n").unwrap();
        let link = root.join("config.json");
        std::os::unix::fs::symlink(&real, &link).unwrap();

        let error = mutate_config("fixture", &link, |_| {
            Ok(MergeResult {
                content: "{\"pwned\":true}\n".into(),
                changed: true,
            })
        })
        .expect_err("symlink must fail");

        assert_eq!(error.code(), "unsafe_config_target");
        assert_eq!(
            fs::read_to_string(&real).unwrap(),
            "{}\n",
            "被拒绝的写入不得穿过链接改动被指向的文件"
        );
        let _ = fs::remove_dir_all(root);
    }

    /// 备份失败与写入失败必须落到**两个不同**的稳定错误码上。
    ///
    /// 端口化之后这两步被 `ConnectorFs::write_atomic` 合成了一次调用、只回一个
    /// 错误串，`mutate_config_inner` 靠 `CONFIG_WRITE_LABELS.backup_failure` 前缀
    /// 分辨。这条测试就是那个前缀判据的守卫：有人"顺手统一"了备份文案，备份失败
    /// 会被静默报成 config_write_failed。
    #[cfg(unix)]
    #[test]
    fn mutate_config_separates_backup_failure_from_write_failure_codes() {
        use std::os::unix::fs::PermissionsExt;

        // 备份失败：备份目标位置放一个非空目录 → fs::copy 必然失败。
        let root = test_dir("backup-failure");
        let path = root.join("config.json");
        fs::write(&path, "{}\n").unwrap();
        fs::create_dir_all(root.join("config.json.superdev-bak").join("occupied")).unwrap();
        let error = mutate_config("fixture", &path, |_| {
            Ok(MergeResult {
                content: "{\"v\":2}\n".into(),
                changed: true,
            })
        })
        .expect_err("backup must fail");
        assert_eq!(error.code(), "config_backup_failed");
        assert!(
            error.message().starts_with("备份配置文件失败: "),
            "备份失败文案必须逐字节复原端口化之前的写法: {}",
            error.message()
        );
        assert_eq!(
            fs::read_to_string(&path).unwrap(),
            "{}\n",
            "备份失败必须在写入之前中止，原文件不能被改动"
        );
        let _ = fs::remove_dir_all(root);

        // 写入失败：父目录只读 + 目标尚不存在（因此根本不会走备份那一步）。
        let root = test_dir("write-failure");
        let locked = root.join("locked");
        fs::create_dir_all(&locked).unwrap();
        fs::set_permissions(&locked, fs::Permissions::from_mode(0o555)).unwrap();
        // root 用户无视目录权限位，这条分支在那种环境下造不出来；探测一次而不是
        // 假设，避免在 root 容器里给出一条假绿。
        let writable_anyway = fs::write(locked.join(".probe"), "x").is_ok();
        if !writable_anyway {
            let error = mutate_config("fixture", &locked.join("config.json"), |_| {
                Ok(MergeResult {
                    content: "{}\n".into(),
                    changed: true,
                })
            })
            .expect_err("write into a read-only directory must fail");
            assert_eq!(error.code(), "config_write_failed");
            assert!(
                !error.message().starts_with("备份"),
                "目标不存在时根本不该走备份分支: {}",
                error.message()
            );
        }
        let _ = fs::set_permissions(&locked, fs::Permissions::from_mode(0o755));
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn mutate_config_is_idempotent_when_transform_reports_unchanged() {
        let root = test_dir("unchanged");
        let path = root.join("config.json");
        fs::write(&path, "{}\n").unwrap();
        let outcome = mutate_config("fixture", &path, |existing| {
            Ok(MergeResult {
                content: existing.unwrap_or("").to_string(),
                changed: false,
            })
        })
        .expect("unchanged ok");
        assert!(!outcome.changed);
        assert_eq!(outcome.backup_path, None);
        assert_eq!(fs::read_to_string(&path).unwrap(), "{}\n");
        let _ = fs::remove_dir_all(root);
    }

    #[cfg(unix)]
    #[test]
    fn mutate_config_preserves_existing_mode_and_restricts_new_files() {
        use std::os::unix::fs::PermissionsExt;

        let root = test_dir("config-mode");
        let existing = root.join("existing.json");
        fs::write(&existing, "{}\n").unwrap();
        fs::set_permissions(&existing, fs::Permissions::from_mode(0o600)).unwrap();
        mutate_config("fixture", &existing, |_| {
            Ok(MergeResult {
                content: "{\"changed\":true}\n".into(),
                changed: true,
            })
        })
        .unwrap();
        assert_eq!(
            fs::metadata(&existing).unwrap().permissions().mode() & 0o777,
            0o600
        );

        let new_file = root.join("new.json");
        mutate_config("fixture", &new_file, |_| {
            Ok(MergeResult {
                content: "{\"created\":true}\n".into(),
                changed: true,
            })
        })
        .unwrap();
        assert_eq!(
            fs::metadata(&new_file).unwrap().permissions().mode() & 0o777,
            0o600
        );
        let _ = fs::remove_dir_all(root);
    }

    #[test]
    fn manual_hook_result_requires_a_nonblank_message() {
        let result = manual_hook_result(None);
        assert_eq!(result.result, IntegrationResult::NeedsAction);
        assert_eq!(result.capability, IntegrationCapability::SessionHook);
        assert!(result
            .message
            .as_ref()
            .is_some_and(|message| !message.trim().is_empty()));
    }
}
