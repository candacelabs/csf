//! Documentation acceptance uses the existing renderer; it owns no second renderer.
use crate::{Credentials, Invocation, ProcessRunner};
use anyhow::{bail, Context, Result};
use clap::Subcommand;
use std::fs;
use std::path::{Path, PathBuf};

#[derive(Subcommand, Debug, PartialEq, Eq)]
pub(crate) enum Action {
    /// Run dependency checks, all documentation tests, full rendering, and artifact checks.
    Check {
        /// Checkout containing docsite/build_docs.py and its complete source inventory.
        #[arg(long)]
        repo: PathBuf,
        /// Fresh or CSF-owned staging directory, outside the source checkout.
        #[arg(long)]
        work: PathBuf,
        /// Fresh or CSF-owned rendered-site directory, outside the source checkout.
        #[arg(long)]
        site: PathBuf,
        /// Python interpreter containing the renderer and CLI dependencies.
        #[arg(long, default_value = "python3")]
        python: PathBuf,
    },
}

pub(crate) fn run(action: Action, runner: &impl ProcessRunner) -> Result<()> {
    let Action::Check {
        repo,
        work,
        site,
        python,
    } = action;
    let repo = repo
        .canonicalize()
        .context("documentation checkout is unavailable")?;
    let renderer = repo.join("docsite/build_docs.py");
    if !renderer.is_file() {
        bail!("documentation renderer is unavailable: {}; supply the full documentation checkout with --repo", renderer.display());
    }
    let work = resolve_output_path(&work)?;
    let site = resolve_output_path(&site)?;
    if work.starts_with(&repo)
        || site.starts_with(&repo)
        || repo.starts_with(&work)
        || repo.starts_with(&site)
        || work.starts_with(&site)
        || site.starts_with(&work)
    {
        bail!("documentation work and site directories must be separate and outside the source checkout");
    }
    prepare_outputs(&repo, &work, &site)?;
    let report = work.join("acceptance-tests.xml");
    if report.exists() {
        fs::remove_file(&report)?;
    }
    let values = Credentials::new();
    println!("[RUN] Checking documentation dependencies.");
    runner.stream(
        &Invocation::new(
            python.as_os_str(),
            ["-c", "import tree_sitter_csf, tree_sitter, markdown"],
        ),
        &values,
    )?;
    println!("[PASS] Documentation dependencies are available.");
    println!("[RUN] Running documentation and CSF consumer tests.");
    runner.stream(
        &Invocation::new(
            python.as_os_str(),
            [
                "-m".into(),
                "pytest".into(),
                repo.join("docsite/tests").into_os_string(),
                repo.join("candace/csf/editor/tests").into_os_string(),
                "-q".into(),
                "-p".into(),
                "no:cacheprovider".into(),
                "--junitxml".into(),
                report.clone().into_os_string(),
            ],
        ),
        &values,
    )?;
    let results = fs::read_to_string(&report)
        .context("documentation tests did not produce their acceptance report")?;
    if results.contains("<skipped") {
        bail!(
            "documentation acceptance does not permit skipped tests; inspect {}",
            report.display()
        );
    }
    println!("[PASS] All documentation tests passed without skips.");
    println!("[RUN] Rendering the complete site, CLI reference, and Go API pages.");
    runner.stream(
        &Invocation::new(
            python.as_os_str(),
            [
                renderer.into_os_string(),
                "build".into(),
                "--repo".into(),
                repo.into_os_string(),
                "--work".into(),
                work.into_os_string(),
                "--site".into(),
                site.clone().into_os_string(),
            ],
        ),
        &values,
    )?;
    verify_site(&site)?;
    println!("[PASS] Complete documentation verified: {}", site.display());
    Ok(())
}

// Resolve existing symlinks before validating overlaps, without creating a
// directory inside an input tree merely to discover that it is forbidden.
fn resolve_output_path(path: &Path) -> Result<PathBuf> {
    let absolute = std::path::absolute(path)?;
    match fs::symlink_metadata(&absolute) {
        Ok(_) => absolute
            .canonicalize()
            .with_context(|| format!("cannot resolve documentation output {}", absolute.display())),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            let parent = absolute
                .parent()
                .context("documentation output needs a parent directory")?;
            let name = absolute
                .file_name()
                .context("unresolved documentation output must end in a directory name")?;
            Ok(resolve_output_path(parent)?.join(name))
        }
        Err(error) => Err(error.into()),
    }
}

fn prepare_outputs(repo: &Path, work: &Path, site: &Path) -> Result<()> {
    let receipt = work.join(".csf-docs-output.json");
    let expected =
        serde_json::to_vec(&serde_json::json!({"version": 1, "repo": repo, "site": site}))?;
    let owned = fs::read(&receipt).ok().as_deref() == Some(expected.as_slice());
    for path in [work, site] {
        if path.exists() && fs::read_dir(path)?.next().is_some() && !owned {
            bail!("refusing to overwrite nonempty documentation output without a matching CSF ownership receipt: {}; choose fresh work and site directories", path.display());
        }
    }
    fs::create_dir_all(work)?;
    fs::create_dir_all(site)?;
    fs::write(receipt, expected)?;
    Ok(())
}

fn verify_site(site: &Path) -> Result<()> {
    for page in [
        "index.html",
        "generated/cli/index.html",
        "generated/go/candace/pkg/core/index.html",
        "generated/go/pkg/provenance/index.html",
    ] {
        let path = site.join(page);
        if !path.is_file() || fs::metadata(&path)?.len() == 0 {
            bail!(
                "required documentation artifact is missing or empty: {}",
                path.display()
            );
        }
    }
    let index = fs::read_to_string(site.join("index.html"))?;
    if !index.contains(" @ ") || !index.contains("rendered ") {
        bail!("rendered documentation index is missing source/build provenance");
    }
    let highlighted = fs::read_to_string(site.join("candace/csf/editor/index.html"))
        .context("CSF highlighting documentation page is missing")?;
    if !highlighted.contains("<span class=\"k\">architecture</span>") {
        bail!("rendered CSF documentation does not contain the architecture keyword highlight");
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::ProcessOutput;
    use std::cell::RefCell;
    use tempfile::TempDir;

    struct Renderer {
        calls: RefCell<Vec<Invocation>>,
        skipped: bool,
        omit_go: bool,
        fail: bool,
    }

    impl ProcessRunner for Renderer {
        fn capture(&self, _: &Invocation) -> Result<ProcessOutput> {
            unreachable!()
        }
        fn stream_with_stdin(&self, _: &Invocation, _: &Credentials) -> Result<()> {
            unreachable!()
        }
        fn stream(&self, invocation: &Invocation, _: &Credentials) -> Result<()> {
            self.calls.borrow_mut().push(invocation.clone());
            if self.fail {
                bail!("dependency import failed");
            }
            let value = |flag: &str| -> Option<PathBuf> {
                invocation
                    .args
                    .windows(2)
                    .find(|pair| pair[0] == flag)
                    .map(|pair| PathBuf::from(&pair[1]))
            };
            if let Some(report) = value("--junitxml") {
                fs::write(
                    report,
                    if self.skipped {
                        "<testsuites><testsuite><testcase><skipped /></testcase></testsuite></testsuites>"
                    } else {
                        "<testsuites><testsuite><testcase /></testsuite></testsuites>"
                    },
                )?;
            }
            if let Some(site) = value("--site") {
                for page in [
                    "index.html",
                    "generated/cli/index.html",
                    "generated/go/candace/pkg/core/index.html",
                    "generated/go/pkg/provenance/index.html",
                    "candace/csf/editor/index.html",
                ] {
                    if self.omit_go && page.contains("/go/") {
                        continue;
                    }
                    let path = site.join(page);
                    fs::create_dir_all(path.parent().unwrap())?;
                    fs::write(
                        path,
                        "source @ unknown · rendered today <span class=\"k\">architecture</span>",
                    )?;
                }
            }
            Ok(())
        }
    }

    fn fixture(root: &Path) -> (Action, Renderer) {
        let repo = root.join("source checkout");
        fs::create_dir_all(repo.join("docsite")).unwrap();
        fs::write(repo.join("docsite/build_docs.py"), "# existing renderer").unwrap();
        (
            Action::Check {
                repo,
                work: root.join("work area"),
                site: root.join("site area"),
                python: "python3".into(),
            },
            Renderer {
                calls: RefCell::new(vec![]),
                skipped: false,
                omit_go: false,
                fail: false,
            },
        )
    }

    #[test]
    fn complete_check_uses_full_renderer_and_preserves_path_arguments() {
        let root = TempDir::new().unwrap();
        let (action, runner) = fixture(root.path());
        run(action, &runner).unwrap();
        let calls = runner.calls.borrow();
        assert_eq!(calls.len(), 3);
        assert!(calls[1].args.contains(
            &root
                .path()
                .join("source checkout/docsite/tests")
                .into_os_string()
        ));
        assert!(calls[2].args.contains(
            &root
                .path()
                .join("source checkout/docsite/build_docs.py")
                .into_os_string()
        ));
        assert!(!calls[2]
            .args
            .iter()
            .any(|arg| arg.to_string_lossy().starts_with("--skip")));
    }

    #[test]
    fn skipped_tests_fail_before_rendering() {
        let root = TempDir::new().unwrap();
        let (action, mut runner) = fixture(root.path());
        runner.skipped = true;
        assert!(run(action, &runner)
            .unwrap_err()
            .to_string()
            .contains("skipped tests"));
        assert_eq!(runner.calls.borrow().len(), 2);
    }

    #[test]
    fn missing_generated_go_pages_fail_acceptance() {
        let root = TempDir::new().unwrap();
        let (action, mut runner) = fixture(root.path());
        runner.omit_go = true;
        assert!(run(action, &runner)
            .unwrap_err()
            .to_string()
            .contains("required documentation artifact"));
    }

    #[test]
    fn dependency_failure_stops_before_tests_or_rendering() {
        let root = TempDir::new().unwrap();
        let (action, mut runner) = fixture(root.path());
        runner.fail = true;
        assert!(run(action, &runner).is_err());
        assert_eq!(runner.calls.borrow().len(), 1);
    }

    #[test]
    fn absent_private_renderer_is_an_explicit_error() {
        let root = TempDir::new().unwrap();
        let (action, runner) = fixture(root.path());
        fs::remove_file(root.path().join("source checkout/docsite/build_docs.py")).unwrap();
        assert!(run(action, &runner)
            .unwrap_err()
            .to_string()
            .contains("renderer is unavailable"));
        assert!(runner.calls.borrow().is_empty());
    }

    #[test]
    fn provenance_and_highlighting_are_required() {
        let root = TempDir::new().unwrap();
        let (action, runner) = fixture(root.path());
        run(action, &runner).unwrap();
        let site = root.path().join("site area");
        fs::write(site.join("candace/csf/editor/index.html"), "plain text").unwrap();
        assert!(verify_site(&site)
            .unwrap_err()
            .to_string()
            .contains("keyword highlight"));
        fs::write(site.join("index.html"), "no provenance").unwrap();
        assert!(verify_site(&site)
            .unwrap_err()
            .to_string()
            .contains("provenance"));
    }

    #[test]
    fn source_overlap_is_rejected_without_creating_directories() {
        let root = TempDir::new().unwrap();
        let (mut action, runner) = fixture(root.path());
        let Action::Check { repo, work, .. } = &mut action;
        *work = repo.join("new-output/child");
        let forbidden = work.clone();
        assert!(run(action, &runner)
            .unwrap_err()
            .to_string()
            .contains("outside the source checkout"));
        assert!(!forbidden.parent().unwrap().exists());
        assert!(runner.calls.borrow().is_empty());
    }

    #[test]
    fn unrelated_output_is_preserved_and_owned_outputs_can_be_reused() {
        let root = TempDir::new().unwrap();
        let (action, runner) = fixture(root.path());
        let site = root.path().join("site area");
        fs::create_dir_all(&site).unwrap();
        fs::write(site.join("important.txt"), "preserve").unwrap();
        assert!(run(action, &runner)
            .unwrap_err()
            .to_string()
            .contains("refusing to overwrite"));
        assert_eq!(
            fs::read_to_string(site.join("important.txt")).unwrap(),
            "preserve"
        );
        assert!(!root.path().join("work area").exists());
        fs::remove_file(site.join("important.txt")).unwrap();
        let (action, runner) = fixture(root.path());
        run(action, &runner).unwrap();
        let (action, runner) = fixture(root.path());
        run(action, &runner).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn symlink_alias_of_source_is_rejected() {
        let root = TempDir::new().unwrap();
        let (mut action, runner) = fixture(root.path());
        let Action::Check { repo, site, .. } = &mut action;
        let alias = root.path().join("alias");
        std::os::unix::fs::symlink(repo, &alias).unwrap();
        *site = alias.join("new-site");
        assert!(run(action, &runner)
            .unwrap_err()
            .to_string()
            .contains("outside the source checkout"));
        assert!(!alias.join("new-site").exists());
    }
}
