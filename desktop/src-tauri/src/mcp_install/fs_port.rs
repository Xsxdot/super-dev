// fs_port.rs 提供 connector 安装逻辑与文件系统之间的端口抽象。
//
// 职责：
//   - 定义 ConnectorFs：connector 安装/卸载所需的全部文件操作原语
//   - 提供 LocalFs：在桌面端本机文件系统上的实现
//
// 边界：
//   - 不含任何智能体方言知识（不知道某个 Agent 的配置长什么样、怎么合并）
//   - 不决定任何路径（路径由调用方算好后传进来）
//   - 不做路径白名单（白名单是服务端职责，远端实现由 agent 端点把关）

use std::fs;
use std::path::{Path, PathBuf};

/// LOCAL_WRITE_KIND 是 LocalFs 传给 atomic_write_file 的写入种类标签。
///
/// 端口本身不知道自己在写「配置文件」还是「hook 配置文件」——那是调用方的
/// 方言知识，不能泄漏进端口。因此本地实现统一用中性标签，具体是哪一类文件由
/// 调用方在自己的日志/错误上下文里体现。
const LOCAL_WRITE_KIND: &str = "文件";

/// FsStat 描述一个路径的存在性与类型。
///
/// 只保留安装逻辑真正需要的两个位：是否存在、是否是目录。
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct FsStat {
    /// exists 表示路径当前是否存在。
    pub exists: bool,
    /// is_dir 表示路径存在且是目录。
    pub is_dir: bool,
}

/// BatchFile 描述一次批量写入中的单个文件。
///
/// rel_path 相对批量写入的根目录；executable 只表达「要不要执行位」这一个语义，
/// 不透传源文件的完整权限位——远端写入端点同样只接受这一个布尔量。
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct BatchFile {
    /// rel_path 是相对目标目录的路径。
    pub rel_path: PathBuf,
    /// content 是文件的完整字节内容。
    pub content: Vec<u8>,
    /// executable 为 true 时目标文件需要带执行位。
    pub executable: bool,
}

/// ConnectorFs 是 connector 安装逻辑与文件系统之间的端口。
///
/// 安装逻辑（哪个智能体的配置怎么合并、skill 装到哪、hook 怎么写）全部在
/// 调用方，本 trait 只提供哑的文件原语，从而让同一套安装逻辑既能跑在本机
/// （LocalFs），也能跑在远端机器（RemoteAgentFs，走 agent 的受限文件端点）。
///
/// 错误一律返回「裸的原因串」，不带业务名词前缀——调用方负责补上
/// 「读取配置文件失败: {原因}」这类上下文，保证本地/远端错误文案一致。
pub trait ConnectorFs {
    /// stat 返回路径的存在性与类型；路径不存在时返回 exists=false 而不是错误。
    fn stat(&self, path: &Path) -> Result<FsStat, String>;

    /// read_optional 读取 UTF-8 文本；路径不存在时返回 Ok(None)。
    fn read_optional(&self, path: &Path) -> Result<Option<String>, String>;

    /// write_atomic 原子写入文本，backup=true 且目标已存在时先备份。
    ///
    /// 返回备份文件路径（未备份时为 None）。备份命名规则由 backup_path 决定，
    /// 两端必须逐字节一致，否则同一台机器上本地装与远端装会留下两套备份名。
    fn write_atomic(
        &self,
        path: &Path,
        content: &str,
        backup: bool,
    ) -> Result<Option<String>, String>;

    /// mkdir_all 递归创建目录；已存在视为成功。
    fn mkdir_all(&self, path: &Path) -> Result<(), String>;

    /// write_batch 把一批文件写进 dir，按需创建各文件的父目录。
    fn write_batch(&self, dir: &Path, files: &[BatchFile]) -> Result<(), String>;

    /// rename 把 from 改名为 to。
    fn rename(&self, from: &Path, to: &Path) -> Result<(), String>;

    /// list_relative_files 递归列出 dir 下的全部普通文件（相对 dir，稳定排序）。
    fn list_relative_files(&self, dir: &Path) -> Result<Vec<PathBuf>, String>;

    /// remove_dir_all 递归删除目录。
    fn remove_dir_all(&self, path: &Path) -> Result<(), String>;
}

/// LocalFs 是 ConnectorFs 在桌面端本机文件系统上的实现。
///
/// 全部方法委托 mcp_install 既有的安全原语（原子写、备份命名、目录遍历），
/// 保证收拢到端口之后本地安装行为与改造前逐字节一致。
pub struct LocalFs;

impl ConnectorFs for LocalFs {
    fn stat(&self, path: &Path) -> Result<FsStat, String> {
        match fs::metadata(path) {
            Ok(metadata) => Ok(FsStat {
                exists: true,
                is_dir: metadata.is_dir(),
            }),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(FsStat {
                exists: false,
                is_dir: false,
            }),
            Err(error) => Err(error.to_string()),
        }
    }

    fn read_optional(&self, path: &Path) -> Result<Option<String>, String> {
        match fs::read_to_string(path) {
            Ok(content) => Ok(Some(content)),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(None),
            Err(error) => Err(error.to_string()),
        }
    }

    fn write_atomic(
        &self,
        path: &Path,
        content: &str,
        backup: bool,
    ) -> Result<Option<String>, String> {
        // 只有目标已存在时才有旧内容可备份；与改造前 install_to_path /
        // install_session_hook 里的 `if path.exists()` 判断保持同一语义。
        let backup_path = if backup && path.exists() {
            let backup = super::backup_path(path);
            fs::copy(path, &backup).map_err(|error| format!("备份文件失败: {error}"))?;
            Some(backup.to_string_lossy().to_string())
        } else {
            None
        };
        super::atomic_write_file(path, content.as_bytes(), LOCAL_WRITE_KIND)?;
        Ok(backup_path)
    }

    fn mkdir_all(&self, path: &Path) -> Result<(), String> {
        fs::create_dir_all(path).map_err(|error| error.to_string())
    }

    fn write_batch(&self, dir: &Path, files: &[BatchFile]) -> Result<(), String> {
        for file in files {
            // write_batch 的契约是「写进 dir 之内」；rel_path 一旦含 `..` 或是
            // 绝对路径就会写到 dir 之外。远端 write-batch 端点对同一件事做了校验，
            // 本地实现不能只靠调用方自觉——这是契约自检，不是路径白名单。
            ensure_contained_relative_path(&file.rel_path)?;
            let target = dir.join(&file.rel_path);
            if let Some(parent) = target.parent() {
                fs::create_dir_all(parent).map_err(|error| {
                    format!("创建文件目录失败 {}: {error}", file.rel_path.display())
                })?;
            }
            super::atomic_write_file(&target, &file.content, LOCAL_WRITE_KIND)?;
            apply_batch_permissions(&target, file.executable)?;
        }
        Ok(())
    }

    fn rename(&self, from: &Path, to: &Path) -> Result<(), String> {
        fs::rename(from, to).map_err(|error| error.to_string())?;
        // rename 成功即操作已生效；父目录 fsync 只是持久化增强，失败不该把一次
        // 已经成功的替换回滚成错误（调用方在替换失败时会把旧目录恢复回来）。
        if let Err(error) = super::sync_parent_directory(to) {
            tracing::warn!(error = %error, "failed to sync parent directory after rename");
        }
        Ok(())
    }

    fn list_relative_files(&self, dir: &Path) -> Result<Vec<PathBuf>, String> {
        super::collect_relative_files(dir).map_err(|error| error.to_string())
    }

    fn remove_dir_all(&self, path: &Path) -> Result<(), String> {
        fs::remove_dir_all(path).map_err(|error| error.to_string())
    }
}

/// ensure_contained_relative_path 校验批量写入的相对路径不会逃出目标目录。
///
/// 只接受由普通路径段组成的相对路径；绝对路径、根前缀与 `..` 一律拒绝。
fn ensure_contained_relative_path(rel_path: &Path) -> Result<(), String> {
    use std::path::Component;

    if rel_path.components().next().is_none() {
        return Err("批量写入的相对路径为空".to_string());
    }
    for component in rel_path.components() {
        match component {
            Component::Normal(_) | Component::CurDir => {}
            _ => {
                return Err(format!("批量写入的相对路径越界 {}", rel_path.display()));
            }
        }
    }
    Ok(())
}

/// apply_batch_permissions 按 executable 标记落权限位。
///
/// unix 下可执行文件用 0o755、其余用 0o644，与远端 agent 的 write-batch 端点
/// 取值一致；atomic_write_file 对新建文件默认收紧到 0o600，这里必须显式放到
/// 目标权限，否则 skill 目录会比改造前（fs::copy 继承源文件 0644/0755）更紧。
#[cfg(unix)]
fn apply_batch_permissions(target: &Path, executable: bool) -> Result<(), String> {
    use std::os::unix::fs::PermissionsExt;

    let mode = if executable { 0o755 } else { 0o644 };
    fs::set_permissions(target, fs::Permissions::from_mode(mode))
        .map_err(|error| format!("设置文件权限失败: {error}"))
}

#[cfg(not(unix))]
fn apply_batch_permissions(_target: &Path, _executable: bool) -> Result<(), String> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::super::create_unique_test_dir;
    use super::{BatchFile, ConnectorFs, LocalFs};
    use std::fs;
    use std::path::PathBuf;

    fn temp_dir() -> PathBuf {
        create_unique_test_dir("superdev-fs-port-test")
    }

    #[test]
    fn write_atomic_backs_up_existing_target_and_returns_backup_path() {
        let dir = temp_dir();
        let target = dir.join("config.json");
        fs::write(&target, "old").expect("seed target");

        let backup = LocalFs
            .write_atomic(&target, "new", true)
            .expect("write atomic");

        let backup = backup.expect("existing target must be backed up");
        assert_eq!(
            backup,
            dir.join("config.json.superdev-bak").to_string_lossy()
        );
        assert_eq!(fs::read_to_string(&backup).expect("read backup"), "old");
        assert_eq!(fs::read_to_string(&target).expect("read target"), "new");
    }

    #[test]
    fn write_atomic_returns_none_when_target_does_not_exist() {
        let dir = temp_dir();
        let target = dir.join("fresh.json");

        let backup = LocalFs
            .write_atomic(&target, "created", true)
            .expect("write atomic");

        assert_eq!(backup, None);
        assert_eq!(fs::read_to_string(&target).expect("read target"), "created");
    }

    #[test]
    fn write_atomic_skips_backup_when_not_requested() {
        let dir = temp_dir();
        let target = dir.join("config.json");
        fs::write(&target, "old").expect("seed target");

        let backup = LocalFs
            .write_atomic(&target, "new", false)
            .expect("write atomic");

        assert_eq!(backup, None);
        assert!(!dir.join("config.json.superdev-bak").exists());
        assert_eq!(fs::read_to_string(&target).expect("read target"), "new");
    }

    #[test]
    fn read_optional_reports_missing_target_as_none() {
        let dir = temp_dir();
        let present = dir.join("present.json");
        fs::write(&present, "{}\n").expect("seed present");

        assert_eq!(
            LocalFs.read_optional(&present).expect("read present"),
            Some("{}\n".to_string())
        );
        assert_eq!(
            LocalFs
                .read_optional(&dir.join("missing.json"))
                .expect("read missing"),
            None
        );
    }

    #[test]
    fn write_batch_writes_nested_files_and_marks_executables() {
        let dir = temp_dir();
        let root = dir.join("skill");
        LocalFs.mkdir_all(&root).expect("mkdir root");

        LocalFs
            .write_batch(
                &root,
                &[
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
                ],
            )
            .expect("write batch");

        assert_eq!(
            fs::read_to_string(root.join("SKILL.md")).expect("read skill"),
            "# SuperDev\n"
        );
        assert_eq!(
            fs::read_to_string(root.join("hooks").join("run-hook.cmd")).expect("read hook"),
            "#!/bin/sh\n"
        );

        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;

            let hook_mode = fs::metadata(root.join("hooks").join("run-hook.cmd"))
                .expect("hook metadata")
                .permissions()
                .mode()
                & 0o777;
            let doc_mode = fs::metadata(root.join("SKILL.md"))
                .expect("doc metadata")
                .permissions()
                .mode()
                & 0o777;
            assert_eq!(hook_mode, 0o755, "可执行文件必须保留执行位");
            assert_eq!(doc_mode, 0o644, "普通文件保持与旧 fs::copy 一致的常规权限");
        }
    }

    #[test]
    fn write_batch_rejects_relative_paths_escaping_the_target_dir() {
        let dir = temp_dir();
        let root = dir.join("skill");
        LocalFs.mkdir_all(&root).expect("mkdir root");

        let error = LocalFs
            .write_batch(
                &root,
                &[BatchFile {
                    rel_path: PathBuf::from("..").join("escaped.md"),
                    content: b"nope\n".to_vec(),
                    executable: false,
                }],
            )
            .expect_err("escaping rel_path must fail");

        assert!(error.contains("越界"), "错误应说明路径越界: {error}");
        assert!(!dir.join("escaped.md").exists());
    }

    #[test]
    fn list_relative_files_returns_sorted_relative_paths() {
        let dir = temp_dir();
        let root = dir.join("skill");
        fs::create_dir_all(root.join("references")).expect("mkdir references");
        fs::write(root.join("SKILL.md"), "a").expect("write skill");
        fs::write(root.join("references").join("log-tools.md"), "b").expect("write log tools");
        fs::write(root.join("references").join("pipeline.md"), "c").expect("write pipeline");

        let files = LocalFs.list_relative_files(&root).expect("list files");

        assert_eq!(
            files,
            vec![
                PathBuf::from("SKILL.md"),
                PathBuf::from("references").join("log-tools.md"),
                PathBuf::from("references").join("pipeline.md"),
            ]
        );
    }

    #[test]
    fn stat_distinguishes_file_directory_and_missing() {
        let dir = temp_dir();
        let file = dir.join("file.json");
        fs::write(&file, "{}").expect("seed file");

        let file_stat = LocalFs.stat(&file).expect("stat file");
        assert!(file_stat.exists);
        assert!(!file_stat.is_dir);

        let dir_stat = LocalFs.stat(&dir).expect("stat dir");
        assert!(dir_stat.exists);
        assert!(dir_stat.is_dir);

        let missing_stat = LocalFs.stat(&dir.join("missing")).expect("stat missing");
        assert!(!missing_stat.exists);
        assert!(!missing_stat.is_dir);
    }

    #[test]
    fn rename_moves_directory_and_remove_dir_all_deletes_it() {
        let dir = temp_dir();
        let source = dir.join("source");
        let moved = dir.join("moved");
        fs::create_dir_all(&source).expect("mkdir source");
        fs::write(source.join("marker"), "keep").expect("seed marker");

        LocalFs.rename(&source, &moved).expect("rename");
        assert!(!source.exists());
        assert_eq!(
            fs::read_to_string(moved.join("marker")).expect("read marker"),
            "keep"
        );

        LocalFs.remove_dir_all(&moved).expect("remove dir");
        assert!(!moved.exists());
    }
}
