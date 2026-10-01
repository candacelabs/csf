//! Seed private work without mistaking an archive import for an upstream commit.
use crate::{
    ensure_private_directory, ensure_process_success, random_hex, validate_remote_url,
    ProcessOutput,
};
use anyhow::{bail, Context, Result};
use serde_json::Value;
use std::ffi::OsString;
use std::fs;
use std::path::{Path, PathBuf};

type GitResult = Result<ProcessOutput>;

pub(crate) fn prepare(
    source: &Path,
    work: &Path,
    git: &impl Fn(&[OsString]) -> GitResult,
) -> Result<(bool, String)> {
    ensure_private_directory(work)?;
    let identity = archive_identity(source)?;
    let repository = work.join("repository");
    let worktrees = work.join("worktrees");
    if repository.symlink_metadata().is_ok() {
        ensure_private_directory(&repository)?;
        let root = git(&arguments(&[
            "-C",
            "/work/repository",
            "rev-parse",
            "--show-toplevel",
        ]))?;
        if !root.success || root.stdout.trim() != "/work/repository" {
            bail!("CSF Workbench repository state is not a standalone Git root.");
        }
        ensure_private_directory(&worktrees)?;
        return Ok((true, String::new()));
    }

    let remote = if identity.is_none() {
        let root = git(&arguments(&[
            "-C",
            "/source",
            "rev-parse",
            "--show-toplevel",
        ]))?;
        if !root.success || root.stdout.trim() != "/source" {
            return Ok((false, "CSF source must be a standalone Git checkout or a release source archive to enable Workbench.".to_owned()));
        }
        let remote = git(&arguments(&[
            "-C", "/source", "remote", "get-url", "origin",
        ]))?;
        let remote = if remote.success {
            remote.stdout.trim().to_owned()
        } else {
            String::new()
        };
        validate_remote_url(&remote)?;
        remote
    } else {
        String::new()
    };

    let stage = Stage::new(work)?;
    let container_path = format!(
        "/work/{}",
        stage.path.file_name().unwrap().to_string_lossy()
    );
    if let Some(revision) = identity {
        copy_archive(source, source, &stage.path)?;
        checked(git, &["init", "--initial-branch=main", &container_path])?;
        checked(
            git,
            &["-C", &container_path, "add", "--all", "--force", "--", "."],
        )?;
        checked(
            git,
            &[
                "-C",
                &container_path,
                "-c",
                "user.name=CSF archive import",
                "-c",
                "user.email=archive@example.invalid",
                "commit",
                "--no-gpg-sign",
                "--no-verify",
                "-m",
                &format!("Import CSF release source archive {revision}"),
            ],
        )?;
    } else {
        checked(
            git,
            &[
                "clone",
                "--local",
                "--no-hardlinks",
                "--quiet",
                "/source",
                &container_path,
            ],
        )?;
        if remote.is_empty() {
            checked(git, &["-C", &container_path, "remote", "remove", "origin"])?;
        } else {
            checked(
                git,
                &[
                    "-C",
                    &container_path,
                    "remote",
                    "set-url",
                    "origin",
                    &remote,
                ],
            )?;
        }
    }
    ensure_private_directory(&worktrees)?;
    fs::rename(&stage.path, &repository)
        .context("could not publish the private Workbench repository")?;
    Ok((true, String::new()))
}

fn arguments(args: &[&str]) -> Vec<OsString> {
    args.iter().map(OsString::from).collect()
}

fn checked(git: &impl Fn(&[OsString]) -> GitResult, args: &[&str]) -> Result<()> {
    ensure_process_success(&git(&arguments(args))?, "Private Workbench Git preparation")
}

fn archive_identity(source: &Path) -> Result<Option<String>> {
    let marker = source.join(".candace-source.json");
    let metadata = match marker.symlink_metadata() {
        Ok(metadata) => metadata,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(error.into()),
    };
    if !metadata.is_file() || metadata.file_type().is_symlink() || metadata.len() > 256 * 1024 {
        bail!("CSF source archive provenance must be a bounded regular file.");
    }
    let identity: Value = serde_json::from_slice(&fs::read(marker)?)?;
    let hash = |key| {
        identity.get(key).and_then(Value::as_str).filter(|text| {
            text.len() == 40
                && text
                    .bytes()
                    .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
        })
    };
    if identity.get("format_version").and_then(Value::as_u64) != Some(1)
        || identity.get("artifact_kind").and_then(Value::as_str) != Some("source_archive")
        || hash("source_revision").is_none()
        || hash("source_tree").is_none()
        || source.join(".git").symlink_metadata().is_ok()
    {
        bail!("CSF source archive provenance is invalid or conflicts with a Git checkout.");
    }
    Ok(hash("source_revision").map(str::to_owned))
}

fn copy_archive(root: &Path, source: &Path, destination: &Path) -> Result<()> {
    for entry in fs::read_dir(source)? {
        let entry = entry?;
        let path = entry.path();
        let relative = path.strip_prefix(root)?;
        let metadata = path.symlink_metadata()?;
        // These are local installer/Bazel artifacts, never release source.
        if relative == Path::new("tools/csf-operator/target")
            || (source == root
                && entry.file_name().to_string_lossy().starts_with("bazel-")
                && metadata.file_type().is_symlink())
        {
            continue;
        }
        let target = destination.join(entry.file_name());
        if metadata.is_dir() {
            ensure_private_directory(&target)?;
            copy_archive(root, &path, &target)?;
        } else if metadata.is_file() {
            fs::copy(&path, &target)?;
        } else if metadata.file_type().is_symlink() {
            if !path.canonicalize()?.starts_with(root.canonicalize()?) {
                bail!(
                    "CSF archive source symlink escapes the release tree: {}",
                    relative.display()
                );
            }
            let link = fs::read_link(&path)?;
            if link.is_absolute() {
                bail!("CSF archive source symlinks must be relative.");
            }
            #[cfg(unix)]
            std::os::unix::fs::symlink(link, target)?;
            #[cfg(not(unix))]
            bail!("CSF source archive import requires Unix symlinks.");
        } else {
            bail!(
                "CSF source archive contains an unsupported file type: {}",
                relative.display()
            );
        }
    }
    Ok(())
}

struct Stage {
    path: PathBuf,
}
impl Stage {
    fn new(work: &Path) -> Result<Self> {
        let path = work.join(format!(".repository-seed-{}", random_hex(16)?));
        fs::create_dir(&path)?;
        let stage = Self { path };
        ensure_private_directory(&stage.path)?;
        Ok(stage)
    }
}
impl Drop for Stage {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.path);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{effective_gid, effective_uid, Invocation, ProcessRunner, SystemRunner};
    use serde_json::json;
    use std::cell::RefCell;
    use tempfile::TempDir;

    fn output(success: bool, stdout: &str) -> ProcessOutput {
        ProcessOutput {
            success,
            code: Some(if success { 0 } else { 1 }),
            stdout: stdout.to_owned(),
            stderr: String::new(),
        }
    }

    fn fixture(root: &Path) -> (PathBuf, PathBuf) {
        let source = root.join("source");
        let work = root.join("work");
        fs::create_dir_all(source.join("tools/csf-operator/target")).unwrap();
        fs::write(
            source.join("tools/csf-operator/target/build-secret"),
            "excluded",
        )
        .unwrap();
        fs::write(source.join("README.md"), "release contents").unwrap();
        fs::write(
            source.join(".candace-source.json"),
            json!({
                "format_version": 1, "artifact_kind": "source_archive",
                "source_revision": "a".repeat(40), "source_tree": "b".repeat(40),
            })
            .to_string(),
        )
        .unwrap();
        (source, work)
    }

    fn container_git(image: &str, source: &Path, work: &Path, args: &[OsString]) -> GitResult {
        let mut invocation = Invocation::new(
            "docker",
            [
                "run".to_owned(),
                "--rm".to_owned(),
                "--network=none".to_owned(),
                "--user".to_owned(),
                format!("{}:{}", effective_uid(), effective_gid()),
                "--volume".to_owned(),
                format!("{}:/source:ro", source.display()),
                "--volume".to_owned(),
                format!("{}:/work", work.display()),
                "--env".to_owned(),
                "GIT_CONFIG_NOSYSTEM=1".to_owned(),
                "--env".to_owned(),
                "GIT_CONFIG_GLOBAL=/dev/null".to_owned(),
                "--entrypoint".to_owned(),
                "git".to_owned(),
                image.to_owned(),
                "-c".to_owned(),
                "core.hooksPath=/dev/null".to_owned(),
            ],
        );
        invocation.args.extend_from_slice(args);
        SystemRunner.capture(&invocation)
    }

    #[test]
    fn archive_import_preserves_provenance_and_excludes_build_outputs() {
        let temporary = TempDir::new().unwrap();
        let (source, work) = fixture(temporary.path());
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink("README.md", source.join("readme-link")).unwrap();
            std::os::unix::fs::symlink("/unrelated/cache", source.join("bazel-bin")).unwrap();
        }
        // Set this to the pinned Git-containing runtime base for an additional
        // real consumer run. Normal unit tests retain their isolated transport.
        let image = std::env::var("CSF_TEST_GIT_IMAGE").ok();
        let calls = RefCell::new(Vec::new());
        let git = |args: &[OsString]| {
            calls.borrow_mut().push(args.to_vec());
            match &image {
                Some(image) => container_git(image, &source, &work, args),
                None => Ok(output(true, "")),
            }
        };
        assert!(prepare(&source, &work, &git).unwrap().0);
        let repository = work.join("repository");
        assert_eq!(
            fs::read(repository.join(".candace-source.json")).unwrap(),
            fs::read(source.join(".candace-source.json")).unwrap()
        );
        assert_eq!(
            fs::read_to_string(repository.join("README.md")).unwrap(),
            "release contents"
        );
        assert!(!repository.join("tools/csf-operator/target").exists());
        assert!(repository.join("bazel-bin").symlink_metadata().is_err());
        #[cfg(unix)]
        assert_eq!(
            fs::read_link(repository.join("readme-link")).unwrap(),
            Path::new("README.md")
        );
        assert!(calls
            .borrow()
            .iter()
            .any(|args| args.iter().any(|arg| arg == "commit")));
        assert!(!calls
            .borrow()
            .iter()
            .any(|args| args.iter().any(|arg| arg == "clone" || arg == "remote")));
        if let Some(image) = &image {
            let committed = container_git(
                image,
                &source,
                &work,
                &arguments(&[
                    "-C",
                    "/work/repository",
                    "show",
                    "HEAD:.candace-source.json",
                ]),
            )
            .unwrap();
            assert!(committed.success, "{}", committed.stderr);
            assert_eq!(
                committed.stdout,
                fs::read_to_string(source.join(".candace-source.json")).unwrap()
            );
            let remote = container_git(
                image,
                &source,
                &work,
                &arguments(&["-C", "/work/repository", "remote"]),
            )
            .unwrap();
            assert!(remote.success && remote.stdout.is_empty());
            fs::write(repository.join("user-work.txt"), "retain edits").unwrap();
            assert!(prepare(&source, &work, &git).unwrap().0);
            assert_eq!(
                fs::read_to_string(repository.join("user-work.txt")).unwrap(),
                "retain edits"
            );
        }
    }

    #[test]
    fn failed_git_preparation_does_not_publish_partial_repository() {
        let temporary = TempDir::new().unwrap();
        let (source, work) = fixture(temporary.path());
        assert!(prepare(&source, &work, &|_| Ok(output(false, ""))).is_err());
        assert!(!work.join("repository").exists());
        assert_eq!(fs::read_dir(&work).unwrap().count(), 0);
        fs::create_dir(work.join("repository")).unwrap();
        fs::write(work.join("repository/user-work"), "retained").unwrap();
        assert!(prepare(&source, &work, &|_| Ok(output(false, ""))).is_err());
        assert_eq!(
            fs::read_to_string(work.join("repository/user-work")).unwrap(),
            "retained"
        );
    }

    #[test]
    fn invalid_archive_provenance_fails_before_git() {
        let temporary = TempDir::new().unwrap();
        let (source, work) = fixture(temporary.path());
        fs::write(source.join(".candace-source.json"), "{}").unwrap();
        assert!(prepare(&source, &work, &|_| panic!("must not run Git")).is_err());
        assert!(!work.join("repository").exists());
    }

    #[cfg(unix)]
    #[test]
    fn archive_symlink_escape_fails_before_git() {
        let temporary = TempDir::new().unwrap();
        let (source, work) = fixture(temporary.path());
        std::os::unix::fs::symlink(temporary.path(), source.join("outside")).unwrap();
        assert!(prepare(&source, &work, &|_| panic!("must not run Git")).is_err());
        assert!(!work.join("repository").exists());
    }

    #[test]
    fn retained_repository_is_checked_without_reseeding() {
        let temporary = TempDir::new().unwrap();
        let (source, work) = fixture(temporary.path());
        fs::create_dir_all(work.join("repository")).unwrap();
        fs::write(work.join("repository/user-work"), "retained").unwrap();
        let calls = RefCell::new(0);
        assert!(
            prepare(&source, &work, &|args| {
                *calls.borrow_mut() += 1;
                assert_eq!(
                    args,
                    arguments(&["-C", "/work/repository", "rev-parse", "--show-toplevel"])
                );
                Ok(output(true, "/work/repository\n"))
            })
            .unwrap()
            .0
        );
        assert_eq!(*calls.borrow(), 1);
        assert!(!work.join("repository/README.md").exists());
    }
}
