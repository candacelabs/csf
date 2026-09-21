use anyhow::{anyhow, bail, Context, Result};
use base64::Engine;
use clap::{Parser, Subcommand};
use reqwest::blocking::Client;
use reqwest::header::{ACCEPT, CONTENT_TYPE};
use reqwest::Method;
use serde_json::{json, Value};
use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::ffi::{OsStr, OsString};
use std::fs::{self, OpenOptions};
use std::io::{BufRead, BufReader, Write};
use std::net::TcpListener;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::thread;
use std::time::{Duration, Instant};
use url::Url;

#[cfg(unix)]
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};

const PROJECT: &str = "csf";
const RUNTIME_SERVICE: &str = "runtime";
const MODEL_NAME: &str = "huggingface/sentence-transformers/all-MiniLM-L6-v2";
const MODEL_SHA256: &str = "25e2858993cd477936f24e412a508b005aa6b59a308301cc69690e4b90cab439";
const CORE_SERVICES: &[&str] = &[
    "opensearch",
    "langfuse-web",
    "langfuse-worker",
    "postgres",
    "clickhouse",
    "minio",
    "redis",
];
const LOG_SERVICES: &[&str] = &[
    "opensearch",
    "langfuse-web",
    "langfuse-worker",
    "postgres",
    "clickhouse",
    "minio",
    "redis",
    "dashboards",
    RUNTIME_SERVICE,
];
const COPILOT_TOKEN_ENVIRONMENTS: &[&str] = &["COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"];

type Credentials = BTreeMap<String, String>;

#[derive(Parser, Debug)]
#[command(
    name = "candace",
    about = "Operate Candace applications",
    disable_help_subcommand = true
)]
struct Cli {
    #[command(subcommand)]
    command: CommandGroup,
}

#[derive(Subcommand, Debug, PartialEq, Eq)]
enum CommandGroup {
    /// Run the complete standalone CSF composition.
    Csf {
        #[command(subcommand)]
        action: Option<Action>,
    },
}

#[derive(Subcommand, Debug, PartialEq, Eq)]
enum Action {
    /// Build and start CSF, initialize dependencies, and wait for readiness.
    Up {
        /// Validate and print the startup plan without creating state or calling Docker.
        #[arg(long)]
        dry: bool,
    },
    /// Stop the standalone project while retaining its volumes.
    Down,
    /// Show the standalone CSF project status.
    Status,
    /// Read redacted standalone CSF logs.
    Logs {
        #[arg(long, value_parser = clap::builder::PossibleValuesParser::new(LOG_SERVICES))]
        service: Option<String>,
        #[arg(long, default_value_t = 100)]
        tail: i64,
    },
    /// Print the private agent MCP bearer key.
    Key,
    /// Call a generated CSF operation inside the runtime container.
    Call { operation: String },
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct Invocation {
    program: OsString,
    args: Vec<OsString>,
}

impl Invocation {
    fn new(
        program: impl Into<OsString>,
        args: impl IntoIterator<Item = impl Into<OsString>>,
    ) -> Self {
        Self {
            program: program.into(),
            args: args.into_iter().map(Into::into).collect(),
        }
    }
}

#[derive(Debug)]
struct ProcessOutput {
    success: bool,
    code: Option<i32>,
    stdout: String,
    stderr: String,
}

trait ProcessRunner {
    fn capture(&self, invocation: &Invocation) -> Result<ProcessOutput>;
    fn stream(&self, invocation: &Invocation, values: &Credentials) -> Result<()>;
    fn stream_with_stdin(&self, invocation: &Invocation, values: &Credentials) -> Result<()>;
}

#[derive(Clone, Copy)]
struct SystemRunner;

impl ProcessRunner for SystemRunner {
    fn capture(&self, invocation: &Invocation) -> Result<ProcessOutput> {
        let output = Command::new(&invocation.program)
            .args(&invocation.args)
            .output()
            .with_context(|| format!("could not run {}", invocation.program.to_string_lossy()))?;
        Ok(ProcessOutput {
            success: output.status.success(),
            code: output.status.code(),
            stdout: String::from_utf8_lossy(&output.stdout).into_owned(),
            stderr: String::from_utf8_lossy(&output.stderr).into_owned(),
        })
    }

    fn stream(&self, invocation: &Invocation, values: &Credentials) -> Result<()> {
        self.stream_inner(invocation, values, Stdio::null())
    }

    fn stream_with_stdin(&self, invocation: &Invocation, values: &Credentials) -> Result<()> {
        self.stream_inner(invocation, values, Stdio::inherit())
    }
}

impl SystemRunner {
    fn stream_inner(
        &self,
        invocation: &Invocation,
        values: &Credentials,
        stdin: Stdio,
    ) -> Result<()> {
        let mut child = Command::new(&invocation.program)
            .args(&invocation.args)
            .stdin(stdin)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .with_context(|| format!("could not run {}", invocation.program.to_string_lossy()))?;

        let stdout = child
            .stdout
            .take()
            .context("process stdout was unavailable")?;
        let stderr = child
            .stderr
            .take()
            .context("process stderr was unavailable")?;
        let values_for_stderr = values.clone();
        let stderr_thread = thread::spawn(move || {
            for line in BufReader::new(stderr).lines() {
                match line {
                    Ok(line) => eprintln!("{}", redact(&line, &values_for_stderr)),
                    Err(error) => return Err(error),
                }
            }
            Ok(())
        });
        for line in BufReader::new(stdout).lines() {
            println!("{}", redact(&line?, values));
        }
        stderr_thread
            .join()
            .map_err(|_| anyhow!("process stderr reader panicked"))??;
        let status = child.wait()?;
        if !status.success() {
            bail!(
                "CSF command failed with exit {}.",
                status
                    .code()
                    .map_or_else(|| "unknown".to_owned(), |code| code.to_string())
            );
        }
        Ok(())
    }
}

struct Operator<R> {
    runner: R,
    source_root: PathBuf,
    infra_root: PathBuf,
    state_dir: PathBuf,
    http: Client,
}

#[derive(Debug)]
struct WorkbenchStatus {
    enabled: bool,
    reason: String,
    copilot_token_changed: bool,
}

pub fn run<I, T>(args: I) -> Result<()>
where
    I: IntoIterator<Item = T>,
    T: Into<OsString> + Clone,
{
    let cli = Cli::try_parse_from(args)?;
    let CommandGroup::Csf { action } = cli.command;
    let action = action.unwrap_or(Action::Up { dry: true });
    if action == Action::Key {
        println!("{}", read_agent_mcp_key(&state_dir()?)?);
        return Ok(());
    }

    let source_root = discover_source_root()?;
    let operator = Operator::new(SystemRunner, source_root, state_dir()?)?;
    operator.execute(action)
}

impl<R: ProcessRunner> Operator<R> {
    fn new(runner: R, source_root: PathBuf, state_dir: PathBuf) -> Result<Self> {
        let infra_root = source_root.join("infra");
        if !infra_root.join("docker-compose.yaml").is_file() {
            bail!(
                "CSF infrastructure files were not found under {}.",
                infra_root.display()
            );
        }
        Ok(Self {
            runner,
            source_root,
            infra_root,
            state_dir,
            http: Client::builder().timeout(Duration::from_secs(30)).build()?,
        })
    }

    fn execute(&self, action: Action) -> Result<()> {
        match action {
            Action::Up { dry: true } => self.dry_up(),
            Action::Up { dry: false } => self.up(),
            Action::Call { operation } => self.call(&operation),
            Action::Down | Action::Status | Action::Logs { .. } => {
                let credentials_file = self.state_dir.join("credentials.env");
                if !credentials_file.is_file() {
                    println!("CSF has not been initialized; run `candace csf up` first.");
                    return Ok(());
                }
                let values = self.credentials(false)?;
                match action {
                    Action::Down => self.compose_stream(&["down"], &values),
                    Action::Status => self.compose_stream(&["ps", "--all"], &values),
                    Action::Logs { service, tail } => {
                        let bounded_tail = tail.clamp(0, 1000).to_string();
                        let mut args = vec![
                            "logs".to_owned(),
                            "--no-color".to_owned(),
                            "--tail".to_owned(),
                            bounded_tail,
                        ];
                        if let Some(service) = service {
                            args.push(service);
                        }
                        self.compose_stream_owned(&args, &values)
                    }
                    _ => unreachable!(),
                }
            }
            Action::Key => unreachable!(),
        }
    }

    fn dry_up(&self) -> Result<()> {
        let dockerfile = self.source_root.join("app/csf/Dockerfile");
        if !dockerfile.is_file() {
            bail!(
                "Pinned CSF runtime build file is missing: {}",
                dockerfile.display()
            );
        }
        let compose = self.infra_root.join("docker-compose.yaml");
        let compose_source = fs::read_to_string(&compose)?;
        if !compose_source.contains("name: csf") || !compose_source.contains("  runtime:") {
            bail!("CSF Compose definition is missing its project or runtime service.");
        }
        let tools: Value =
            serde_json::from_str(&fs::read_to_string(self.infra_root.join("mcp-tools.json"))?)?;
        let tool_count = tools
            .get("tools")
            .and_then(Value::as_array)
            .context("mcp-tools.json omitted tools")?
            .len();
        let credentials_path = self.state_dir.join("credentials.env");
        let retained = if credentials_path.is_file() {
            Some(read_credentials_unmodified(&credentials_path)?)
        } else {
            None
        };
        let runtime_address = retained
            .as_ref()
            .and_then(|values| values.get("CSF_RUNTIME_ADDRESS"))
            .map_or("127.0.0.1", String::as_str);
        let runtime_port = retained
            .as_ref()
            .and_then(|values| values.get("CSF_RUNTIME_PORT"))
            .map_or("14111", String::as_str);
        let runtime_port_number: u16 = runtime_port
            .parse()
            .context("invalid retained CSF runtime port")?;
        let runtime_port_available =
            TcpListener::bind((runtime_address, runtime_port_number)).is_ok();
        let runtime_endpoint = format!("http://{runtime_address}:{runtime_port}/");
        println!(
            "{}",
            serde_json::to_string_pretty(&json!({
                "dry": true,
                "would_run": "candace csf up",
                "compose_project": PROJECT,
                "compose_definition": compose,
                "source_root": self.source_root,
                "state": self.state_dir,
                "state_initialized": retained.is_some(),
                "runtime_endpoint": runtime_endpoint,
                "runtime_port_available": runtime_port_available,
                "core_services": CORE_SERVICES,
                "mcp_tool_count": tool_count,
            }))?
        );
        Ok(())
    }

    fn up(&self) -> Result<()> {
        let dockerfile = self.source_root.join("app/csf/Dockerfile");
        if !dockerfile.is_file() {
            bail!(
                "Pinned CSF runtime build file is missing: {}",
                dockerfile.display()
            );
        }
        let mut values = self.credentials(true)?;
        let (evidence, workbench) = self.private_runtime_config(&mut values)?;
        self.preflight_runtime_port(&values)?;

        println!("Starting the private CSF services in the isolated `csf` Compose project.");
        let mut core_args = vec!["up", "-d", "--wait", "--wait-timeout", "300"];
        core_args.extend_from_slice(CORE_SERVICES);
        self.compose_stream(&core_args, &values)?;
        self.bootstrap_search(&values)?;
        self.bootstrap_embeddings(&values)?;
        let model: Value = serde_json::from_str(&fs::read_to_string(
            self.state_dir.join("embedding-model.json"),
        )?)?;
        values.insert(
            "CSF_EMBEDDING_MODEL".to_owned(),
            required_string(&model, "model_id")?.to_owned(),
        );
        write_credentials(&self.state_dir.join("credentials.env"), &values, false)?;
        self.compose_stream(&["build", RUNTIME_SERVICE], &values)?;
        self.initialize_database(&values)?;
        let runtime_args = runtime_up_args(workbench.copilot_token_changed);
        self.compose_stream_owned(&runtime_args, &values)?;
        let endpoint = self.endpoint(&values)?;
        self.wait_ready(&endpoint, Duration::from_secs(240))?;
        println!(
            "{}",
            serde_json::to_string(&json!({
                "ready": true,
                "endpoint": endpoint,
                "evidence": evidence,
                "workbench_scheduling": {
                    "enabled": workbench.enabled,
                    "reason": workbench.reason,
                },
            }))?
        );
        Ok(())
    }

    fn call(&self, operation: &str) -> Result<()> {
        if !self.state_dir.join("credentials.env").is_file() {
            bail!("CSF is not initialized; run `candace csf up` first.");
        }
        let values = self.credentials(false)?;
        let endpoint = format!(
            "http://127.0.0.1:{}",
            credential(&values, "CSF_RUNTIME_PORT")?
        );
        self.compose_stream_owned_with_stdin(
            &[
                "exec".to_owned(),
                "-T".to_owned(),
                RUNTIME_SERVICE.to_owned(),
                "/usr/local/bin/csf".to_owned(),
                "call".to_owned(),
                "--endpoint".to_owned(),
                endpoint,
                operation.to_owned(),
            ],
            &values,
        )
    }

    fn credentials(&self, create: bool) -> Result<Credentials> {
        let path = self.state_dir.join("credentials.env");
        if !path.exists() {
            if !create {
                bail!("CSF credentials are absent; run `candace csf up` first.");
            }
            let output = self.runner.capture(&Invocation::new(
                "docker",
                [
                    "volume",
                    "ls",
                    "--quiet",
                    "--filter",
                    "label=com.docker.compose.project=csf",
                ],
            ))?;
            ensure_process_success(&output, "Docker volume inventory")?;
            if !output.stdout.trim().is_empty() {
                bail!("CSF volumes exist without their original credentials; restore the original CSF state directory.");
            }
            ensure_private_directory(&self.state_dir)?;
            let values = new_credentials()?;
            write_credentials(&path, &values, true)?;
        }
        let mut values = read_credentials(&path)?;
        if create {
            let defaults = default_credentials()?;
            let mut changed = false;
            for (key, value) in defaults {
                if let std::collections::btree_map::Entry::Vacant(entry) = values.entry(key) {
                    entry.insert(value);
                    changed = true;
                }
            }
            if changed {
                write_credentials(&path, &values, false)?;
            }
        }
        Ok(values)
    }

    fn private_runtime_config(
        &self,
        values: &mut Credentials,
    ) -> Result<(PathBuf, WorkbenchStatus)> {
        ensure_private_directory(&self.state_dir)?;
        let evidence = self.state_dir.join("evidence");
        for directory in [
            evidence.as_path(),
            evidence.join("receipts").as_path(),
            evidence.join("cas").as_path(),
        ] {
            ensure_private_directory(directory)?;
        }
        let work = evidence.join("work.json");
        if !work.exists() {
            write_json(&work, &json!({}), true)?;
        }

        let database = self.state_dir.join("csf-database.json");
        let workbench_database = self.state_dir.join("csf-workbench-database.json");
        let workbench_root = self.state_dir.join("workbench");
        ensure_private_directory(&workbench_root)?;
        ensure_private_directory(&workbench_root.join("copilot-home"))?;
        let agent_mcp_key = ensure_agent_mcp_key(&self.state_dir)?;
        let (token_path, token, token_changed) = ensure_copilot_token(&self.state_dir)?;

        let mut database_url =
            Url::parse("postgresql://brain@postgres:5432/brain?sslmode=disable")?;
        database_url
            .set_password(Some(credential(values, "CSF_DB_PASSWORD")?))
            .map_err(|_| anyhow!("CSF database password could not be encoded"))?;
        let database_document = json!({"url": database_url.as_str()});
        write_json(&database, &database_document, true)?;
        write_json(&workbench_database, &database_document, true)?;

        let mut workbench = WorkbenchStatus {
            enabled: false,
            reason: "No Copilot token is available; set COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN and run `candace csf up`.".to_owned(),
            copilot_token_changed: token_changed,
        };
        if !token.is_empty() {
            let (enabled, reason) = self.prepare_workbench_repository(&workbench_root)?;
            workbench.enabled = enabled;
            workbench.reason = reason;
        }

        let workbench_config = if workbench.enabled {
            workbench_database.display().to_string()
        } else {
            String::new()
        };
        values.extend([
            (
                "CSF_SOURCE_ROOT".to_owned(),
                self.source_root.display().to_string(),
            ),
            (
                "CSF_DOCKERFILE".to_owned(),
                self.source_root
                    .join("app/csf/Dockerfile")
                    .display()
                    .to_string(),
            ),
            (
                "CSF_STATE_DIR".to_owned(),
                self.state_dir.display().to_string(),
            ),
            (
                "CSF_EVIDENCE_DIR".to_owned(),
                evidence.display().to_string(),
            ),
            (
                "CSF_DATABASE_CONFIG".to_owned(),
                database.display().to_string(),
            ),
            (
                "CSF_AGENT_MCP_KEY_FILE".to_owned(),
                agent_mcp_key.display().to_string(),
            ),
            (
                "CSF_COPILOT_TOKEN_FILE".to_owned(),
                token_path.display().to_string(),
            ),
            (
                "CSF_WORKBENCH_DATABASE_CONFIG_FILE".to_owned(),
                workbench_database.display().to_string(),
            ),
            ("CSF_WORKBENCH_DATABASE_CONFIG".to_owned(), workbench_config),
            (
                "CSF_WORKBENCH_DIR".to_owned(),
                workbench_root.display().to_string(),
            ),
            ("CSF_UID".to_owned(), effective_uid().to_string()),
            ("CSF_GID".to_owned(), effective_gid().to_string()),
        ]);
        values
            .entry("CSF_RUNTIME_ADDRESS".to_owned())
            .or_insert_with(|| "127.0.0.1".to_owned());
        values
            .entry("CSF_RUNTIME_PORT".to_owned())
            .or_insert_with(|| "14111".to_owned());
        values
            .entry("CSF_EMBEDDING_MODEL".to_owned())
            .or_insert_with(|| "provisioning".to_owned());
        values
            .entry("CSF_SUBNET".to_owned())
            .or_insert_with(|| "10.231.75.0/24".to_owned());
        write_credentials(&self.state_dir.join("credentials.env"), values, false)?;
        Ok((evidence, workbench))
    }

    fn prepare_workbench_repository(&self, workbench_root: &Path) -> Result<(bool, String)> {
        ensure_private_directory(workbench_root)?;
        let repository = workbench_root.join("repository");
        let worktrees = workbench_root.join("worktrees");
        for directory in [&repository, &worktrees] {
            if directory.exists() || fs::symlink_metadata(directory).is_ok() {
                ensure_private_directory(directory)?;
            }
        }

        let source_root = self.git_capture(&["rev-parse", "--show-toplevel"], &self.source_root)?;
        if !source_root.success {
            return Ok((
                false,
                "The CSF source checkout is not a Git repository.".to_owned(),
            ));
        }
        let git_root = absolute_path(Path::new(source_root.stdout.trim()))?;
        if git_root != absolute_path(&self.source_root)? {
            return Ok((
                false,
                "The CSF source is nested in a monorepo; run from a standalone CSF clone to bound Workbench access.".to_owned(),
            ));
        }

        let remote = self.git_capture(&["remote", "get-url", "origin"], &self.source_root)?;
        let source_remote = if remote.success {
            remote.stdout.trim().to_owned()
        } else {
            String::new()
        };
        validate_remote_url(&source_remote)?;

        if repository.exists() {
            let root = self.git_capture(&["rev-parse", "--show-toplevel"], &repository)?;
            if !root.success
                || absolute_path(Path::new(root.stdout.trim()))? != absolute_path(&repository)?
            {
                bail!("CSF Workbench repository state is not a standalone Git root.");
            }
        } else {
            let clone = self.runner.capture(&Invocation::new(
                "git",
                [
                    OsString::from("clone"),
                    OsString::from("--local"),
                    OsString::from("--no-hardlinks"),
                    OsString::from("--quiet"),
                    self.source_root.as_os_str().to_owned(),
                    repository.as_os_str().to_owned(),
                ],
            ))?;
            if !clone.success {
                bail!("Could not create the private CSF Workbench repository.");
            }
            let remote_args = if source_remote.is_empty() {
                vec![
                    "remote".to_owned(),
                    "remove".to_owned(),
                    "origin".to_owned(),
                ]
            } else {
                vec![
                    "remote".to_owned(),
                    "set-url".to_owned(),
                    "origin".to_owned(),
                    source_remote,
                ]
            };
            let output = self.git_capture_owned(&remote_args, &repository)?;
            ensure_process_success(&output, "Git remote configuration")?;
        }
        ensure_private_directory(&worktrees)?;
        Ok((true, String::new()))
    }

    fn git_capture(&self, args: &[&str], directory: &Path) -> Result<ProcessOutput> {
        let mut owned = vec![OsString::from("-C"), directory.as_os_str().to_owned()];
        owned.extend(args.iter().map(OsString::from));
        self.runner.capture(&Invocation::new("git", owned))
    }

    fn git_capture_owned(&self, args: &[String], directory: &Path) -> Result<ProcessOutput> {
        let mut owned = vec![OsString::from("-C"), directory.as_os_str().to_owned()];
        owned.extend(args.iter().map(OsString::from));
        self.runner.capture(&Invocation::new("git", owned))
    }

    fn compose_invocation<T: AsRef<OsStr>>(&self, args: &[T]) -> Invocation {
        let mut full = vec![
            OsString::from("compose"),
            OsString::from("--env-file"),
            self.state_dir.join("credentials.env").into_os_string(),
            OsString::from("-f"),
            self.infra_root.join("docker-compose.yaml").into_os_string(),
            OsString::from("-p"),
            OsString::from(PROJECT),
        ];
        full.extend(args.iter().map(|value| value.as_ref().to_owned()));
        Invocation::new("docker", full)
    }

    fn compose_capture(&self, args: &[&str], values: &Credentials, quiet: bool) -> Result<String> {
        let output = self.runner.capture(&self.compose_invocation(args))?;
        let stdout = redact(&output.stdout, values);
        let stderr = redact(&output.stderr, values);
        if !quiet {
            if !stdout.is_empty() {
                print!("{stdout}");
            }
            if !stderr.is_empty() {
                eprint!("{stderr}");
            }
        }
        ensure_process_success(&output, "CSF Compose action")?;
        Ok(stdout.trim().to_owned())
    }

    fn compose_stream(&self, args: &[&str], values: &Credentials) -> Result<()> {
        self.runner.stream(&self.compose_invocation(args), values)
    }

    fn compose_stream_owned(&self, args: &[String], values: &Credentials) -> Result<()> {
        self.runner.stream(&self.compose_invocation(args), values)
    }

    fn compose_stream_owned_with_stdin(&self, args: &[String], values: &Credentials) -> Result<()> {
        self.runner
            .stream_with_stdin(&self.compose_invocation(args), values)
    }

    fn preflight_runtime_port(&self, values: &Credentials) -> Result<()> {
        let address = credential(values, "CSF_RUNTIME_ADDRESS")?;
        let port: u16 = credential(values, "CSF_RUNTIME_PORT")?.parse()?;
        if TcpListener::bind((address, port)).is_ok() {
            return Ok(());
        }
        let running = self.compose_capture(
            &["ps", "--status", "running", "--quiet", RUNTIME_SERVICE],
            values,
            true,
        )?;
        if !running.is_empty() {
            return Ok(());
        }
        bail!(
            "CSF cannot start: {address}:{port} is already in use. No CSF containers were started."
        )
    }

    fn initialize_database(&self, values: &Credentials) -> Result<()> {
        let existing = self.compose_capture(
            &[
                "exec",
                "-T",
                "postgres",
                "psql",
                "-U",
                "brain",
                "-d",
                "brain",
                "-Atqc",
                "SELECT to_regclass('public.brainspine_runs') IS NOT NULL",
            ],
            values,
            true,
        )?;
        if matches!(existing.to_ascii_lowercase().as_str(), "t" | "true") {
            println!("CSF database schema already exists; preserving it.");
            return Ok(());
        }
        self.compose_stream(
            &[
                "run",
                "--rm",
                "--no-deps",
                RUNTIME_SERVICE,
                "initialize",
                "--database-config",
                "/config/csf-database.json",
            ],
            values,
        )
    }

    fn endpoint(&self, values: &Credentials) -> Result<String> {
        Ok(format!(
            "http://{}:{}/",
            credential(values, "CSF_RUNTIME_ADDRESS")?,
            credential(values, "CSF_RUNTIME_PORT")?
        ))
    }

    fn wait_ready(&self, url: &str, timeout: Duration) -> Result<()> {
        let deadline = Instant::now() + timeout;
        while Instant::now() < deadline {
            if self
                .http
                .get(url)
                .timeout(Duration::from_secs(3))
                .send()
                .is_ok_and(|response| response.status().is_success())
            {
                return Ok(());
            }
            thread::sleep(Duration::from_secs(1));
        }
        bail!("CSF did not become ready at {url}; inspect `candace csf logs`.")
    }

    fn endpoints(&self, values: &Credentials) -> Result<(String, String)> {
        Ok((
            format!(
                "http://127.0.0.1:{}",
                credential(values, "CSF_OPENSEARCH_PORT")?
            ),
            format!(
                "http://{}:{}",
                credential(values, "CSF_LANGFUSE_ADDRESS")?,
                credential(values, "CSF_LANGFUSE_PORT")?
            ),
        ))
    }

    fn request(
        &self,
        url: &str,
        method: Method,
        body: Option<&Value>,
        headers: &[(&str, &str)],
    ) -> Result<Value> {
        let mut request = self
            .http
            .request(method, url)
            .header(CONTENT_TYPE, "application/json");
        for (name, value) in headers {
            request = request.header(*name, *value);
        }
        if let Some(body) = body {
            request = request.json(body);
        }
        let response = request.send()?;
        let status = response.status();
        let content_type = response
            .headers()
            .get(CONTENT_TYPE)
            .and_then(|value| value.to_str().ok())
            .unwrap_or("")
            .to_owned();
        let text = response.text()?;
        if !status.is_success() {
            bail!(
                "HTTP {} {}: {}",
                status.as_u16(),
                url,
                text.chars().take(1200).collect::<String>()
            );
        }
        if text.is_empty() {
            return Ok(json!({}));
        }
        if content_type.starts_with("text/event-stream") {
            for line in text.lines() {
                if let Some(data) = line.strip_prefix("data:") {
                    let event: Value = serde_json::from_str(data.trim())?;
                    if event.get("result").is_some() || event.get("error").is_some() {
                        return Ok(event);
                    }
                }
            }
            bail!("OpenSearch MCP response contained no result event");
        }
        Ok(serde_json::from_str(&text)?)
    }

    fn mcp(&self, url: &str, method: &str, params: Value) -> Result<Value> {
        let response = self.request(
            url,
            Method::POST,
            Some(&json!({
                "jsonrpc": "2.0",
                "id": random_hex(16)?,
                "method": method,
                "params": params,
            })),
            &[
                (ACCEPT.as_str(), "application/json, text/event-stream"),
                ("MCP-Protocol-Version", "2025-03-26"),
            ],
        )?;
        if response.get("error").is_some()
            || response
                .pointer("/result/isError")
                .and_then(Value::as_bool)
                .unwrap_or(false)
        {
            bail!("OpenSearch MCP {method} failed: {response}");
        }
        response
            .get("result")
            .cloned()
            .context("OpenSearch MCP response omitted result")
    }

    fn handshake(&self, url: &str) -> Result<Vec<Value>> {
        self.mcp(
            url,
            "initialize",
            json!({
                "protocolVersion": "2025-03-26",
                "capabilities": {},
                "clientInfo": {"name": "csf-operator", "version": "1"},
            }),
        )?;
        Ok(self
            .mcp(url, "tools/list", json!({}))?
            .get("tools")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default())
    }

    fn ensure_index(&self, url: &str, name: &str, body: Value) -> Result<()> {
        let existing = self.request(
            &format!("{url}/_cat/indices/{name}?format=json&ignore_unavailable=true"),
            Method::GET,
            None,
            &[],
        )?;
        if existing.as_array().is_some_and(Vec::is_empty) {
            self.request(&format!("{url}/{name}"), Method::PUT, Some(&body), &[])?;
        }
        Ok(())
    }

    fn bootstrap_search(&self, values: &Credentials) -> Result<()> {
        let (url, _) = self.endpoints(values)?;
        self.request(
            &format!("{url}/_cluster/settings"),
            Method::PUT,
            Some(&json!({"persistent": {
                "plugins.ml_commons.mcp_server_enabled": true,
                "plugins.ml_commons.only_run_on_ml_node": false,
            }})),
            &[],
        )?;
        let registered: BTreeSet<String> = self
            .handshake(&format!("{url}/_plugins/_ml/mcp"))?
            .into_iter()
            .filter_map(|tool| tool.get("name").and_then(Value::as_str).map(str::to_owned))
            .collect();
        let tools: Value =
            serde_json::from_str(&fs::read_to_string(self.infra_root.join("mcp-tools.json"))?)?;
        let missing: Vec<Value> = tools
            .get("tools")
            .and_then(Value::as_array)
            .context("mcp-tools.json omitted tools")?
            .iter()
            .filter(|tool| {
                tool.get("name")
                    .and_then(Value::as_str)
                    .is_some_and(|name| !registered.contains(name))
            })
            .cloned()
            .collect();
        if !missing.is_empty() {
            self.request(
                &format!("{url}/_plugins/_ml/mcp/tools/_register"),
                Method::POST,
                Some(&json!({"tools": missing})),
                &[],
            )?;
        }
        self.ensure_index(
            &url,
            "brain-logs",
            json!({
                "settings": {"number_of_shards": 1, "number_of_replicas": 0},
                "mappings": {"properties": {
                    "recorded_at": {"type": "date"},
                    "run_id": {"type": "keyword"},
                    "kind": {"type": "keyword"},
                    "level": {"type": "keyword"},
                    "message": {"type": "text"},
                    "evidence_path": {"type": "keyword"},
                    "controller_hash": {"type": "keyword"},
                    "metadata": {"type": "object", "enabled": false},
                }},
            }),
        )?;
        println!("CSF OpenSearch tools and log index are ready.");
        Ok(())
    }

    fn wait_model(&self, url: &str, task_id: &str) -> Result<Value> {
        let deadline = Instant::now() + Duration::from_secs(240);
        while Instant::now() < deadline {
            let task = self.request(
                &format!("{url}/_plugins/_ml/tasks/{task_id}"),
                Method::GET,
                None,
                &[],
            )?;
            match task.get("state").and_then(Value::as_str) {
                Some("COMPLETED") => return Ok(task),
                Some("FAILED" | "COMPLETED_WITH_ERROR") => {
                    bail!("Pinned OpenSearch embedding model deployment failed")
                }
                _ => thread::sleep(Duration::from_secs(2)),
            }
        }
        bail!("Pinned OpenSearch embedding model did not deploy within 240 seconds")
    }

    fn deploy_model(&self, url: &str, model_id: &str) -> Result<()> {
        let task = self.request(
            &format!("{url}/_plugins/_ml/models/{model_id}/_deploy"),
            Method::POST,
            Some(&json!({})),
            &[],
        )?;
        self.wait_model(url, required_string(&task, "task_id")?)?;
        Ok(())
    }

    fn bootstrap_embeddings(&self, values: &Credentials) -> Result<()> {
        let (url, _) = self.endpoints(values)?;
        let path = self.state_dir.join("embedding-model.json");
        let mut model = if path.exists() {
            let model: Value = serde_json::from_str(&fs::read_to_string(&path)?)?;
            let model_id = required_string(&model, "model_id")?;
            let current = self.request(
                &format!("{url}/_plugins/_ml/models/{model_id}"),
                Method::GET,
                None,
                &[],
            )?;
            if current.get("model_state").and_then(Value::as_str) != Some("DEPLOYED") {
                self.deploy_model(&url, model_id)?;
            }
            model
        } else {
            println!("Registering the pinned local MiniLM embedding model in OpenSearch.");
            let task = self.request(
                &format!("{url}/_plugins/_ml/models/_register?deploy=true"),
                Method::POST,
                Some(&json!({
                    "name": MODEL_NAME,
                    "version": "1.0.2",
                    "model_format": "TORCH_SCRIPT",
                })),
                &[],
            )?;
            let done = self.wait_model(&url, required_string(&task, "task_id")?)?;
            json!({
                "model_id": required_string(&done, "model_id")?,
                "name": MODEL_NAME,
                "version": "1.0.2",
                "dimension": 384,
                "format": "TORCH_SCRIPT",
            })
        };
        let model_id = required_string(&model, "model_id")?.to_owned();
        let mut deployed = self.request(
            &format!("{url}/_plugins/_ml/models/{model_id}"),
            Method::GET,
            None,
            &[],
        )?;
        if deployed.get("model_state").and_then(Value::as_str) != Some("DEPLOYED") {
            self.deploy_model(&url, &model_id)?;
            deployed = self.request(
                &format!("{url}/_plugins/_ml/models/{model_id}"),
                Method::GET,
                None,
                &[],
            )?;
        }
        if deployed
            .get("model_content_hash_value")
            .and_then(Value::as_str)
            != Some(MODEL_SHA256)
        {
            bail!("OpenSearch model contents do not match the pinned MiniLM artifact");
        }
        let object = model
            .as_object_mut()
            .context("embedding model state must be a JSON object")?;
        object.insert("sha256".to_owned(), Value::String(MODEL_SHA256.to_owned()));
        object.insert(
            "artifact_bytes".to_owned(),
            deployed
                .get("model_content_size_in_bytes")
                .cloned()
                .context("OpenSearch model omitted content size")?,
        );
        object.insert(
            "model_state".to_owned(),
            deployed
                .get("model_state")
                .cloned()
                .context("OpenSearch model omitted deployment state")?,
        );
        write_json(&path, &model, true)?;
        self.request(
            &format!("{url}/_ingest/pipeline/brain-embedding"),
            Method::PUT,
            Some(&json!({
                "description": "Local MiniLM embeddings for CSF retrieval",
                "processors": [{"text_embedding": {
                    "model_id": model_id,
                    "field_map": {"text": "embedding"},
                }}],
            })),
            &[],
        )?;
        self.ensure_index(
            &url,
            "brain-knowledge",
            json!({
                "settings": {
                    "number_of_shards": 1,
                    "number_of_replicas": 0,
                    "index.knn": true,
                    "default_pipeline": "brain-embedding",
                },
                "mappings": {"properties": {
                    "text": {"type": "text"},
                    "source_id": {"type": "keyword"},
                    "revision": {"type": "keyword"},
                    "content_hash": {"type": "keyword"},
                    "source_uri": {"type": "keyword"},
                    "title": {"type": "text"},
                    "media_type": {"type": "keyword"},
                    "license": {"type": "keyword"},
                    "retrieved_at": {"type": "date"},
                    "artifact_ref": {"type": "keyword"},
                    "size_bytes": {"type": "long"},
                    "kind": {"type": "keyword"},
                    "recorded_at": {"type": "date"},
                    "embedding": {"type": "knn_vector", "dimension": 384, "method": {
                        "name": "hnsw", "engine": "lucene", "space_type": "cosinesimil",
                    }},
                }},
            }),
        )?;
        println!("CSF retrieval model and index are ready.");
        Ok(())
    }
}

fn runtime_up_args(force_recreate: bool) -> Vec<String> {
    let mut args = vec!["up".to_owned(), "-d".to_owned()];
    if force_recreate {
        args.push("--force-recreate".to_owned());
    }
    args.extend([
        "--wait".to_owned(),
        "--wait-timeout".to_owned(),
        "300".to_owned(),
        RUNTIME_SERVICE.to_owned(),
    ]);
    args
}

fn new_credentials() -> Result<Credentials> {
    let mut values = Credentials::new();
    for key in [
        "POSTGRES_PASSWORD",
        "CLICKHOUSE_PASSWORD",
        "SALT",
        "ENCRYPTION_KEY",
        "NEXTAUTH_SECRET",
        "REDIS_AUTH",
        "MINIO_ROOT_PASSWORD",
        "LANGFUSE_USER_PASSWORD",
        "CSF_DB_PASSWORD",
    ] {
        values.insert(key.to_owned(), random_hex(32)?);
    }
    values.extend([
        (
            "LANGFUSE_PUBLIC_KEY".to_owned(),
            format!("pk-lf-{}", random_hex(16)?),
        ),
        (
            "LANGFUSE_SECRET_KEY".to_owned(),
            format!("sk-lf-{}", random_hex(32)?),
        ),
        ("CSF_LANGFUSE_PORT".to_owned(), "14300".to_owned()),
        ("CSF_LANGFUSE_ADDRESS".to_owned(), "127.0.0.1".to_owned()),
        ("CSF_OPENSEARCH_PORT".to_owned(), "19200".to_owned()),
        ("CSF_DASHBOARDS_PORT".to_owned(), "15601".to_owned()),
        ("CSF_MINIO_PORT".to_owned(), "14390".to_owned()),
        ("CSF_POSTGRES_PORT".to_owned(), "15432".to_owned()),
        ("CSF_MLFLOW_PORT".to_owned(), "14500".to_owned()),
        ("CSF_SUBNET".to_owned(), "10.231.75.0/24".to_owned()),
        ("MLFLOW_DB_PASSWORD".to_owned(), random_hex(32)?),
    ]);
    Ok(values)
}

fn default_credentials() -> Result<Credentials> {
    Ok(Credentials::from([
        ("CSF_DB_PASSWORD".to_owned(), random_hex(32)?),
        ("CSF_LANGFUSE_PORT".to_owned(), "14300".to_owned()),
        ("CSF_LANGFUSE_ADDRESS".to_owned(), "127.0.0.1".to_owned()),
        ("CSF_OPENSEARCH_PORT".to_owned(), "19200".to_owned()),
        ("CSF_DASHBOARDS_PORT".to_owned(), "15601".to_owned()),
        ("CSF_MINIO_PORT".to_owned(), "14390".to_owned()),
        ("CSF_POSTGRES_PORT".to_owned(), "15432".to_owned()),
        ("CSF_MLFLOW_PORT".to_owned(), "14500".to_owned()),
        ("CSF_SUBNET".to_owned(), "10.231.75.0/24".to_owned()),
        ("MLFLOW_DB_PASSWORD".to_owned(), random_hex(32)?),
    ]))
}

fn credential<'a>(values: &'a Credentials, key: &str) -> Result<&'a str> {
    values
        .get(key)
        .map(String::as_str)
        .with_context(|| format!("CSF credential file omitted {key}"))
}

fn read_credentials(path: &Path) -> Result<Credentials> {
    let metadata = fs::symlink_metadata(path)
        .context("CSF credentials must be a regular file in the private state directory.")?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        bail!("CSF credentials must be a regular file in the private state directory.");
    }
    #[cfg(unix)]
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    read_credentials_unmodified(path)
}

fn read_credentials_unmodified(path: &Path) -> Result<Credentials> {
    let metadata = fs::symlink_metadata(path)
        .context("CSF credentials must be a regular file in the private state directory.")?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        bail!("CSF credentials must be a regular file in the private state directory.");
    }
    let text = fs::read_to_string(path)?;
    if text.is_empty() || text.len() > 64 * 1024 {
        bail!("CSF credentials file is empty or malformed.");
    }
    let mut values = Credentials::new();
    for line in text
        .lines()
        .filter(|line| !line.is_empty() && !line.starts_with('#'))
    {
        let (key, value) = line
            .split_once('=')
            .with_context(|| format!("Malformed CSF credential line for {path:?}"))?;
        values.insert(key.to_owned(), value.to_owned());
    }
    Ok(values)
}

fn write_credentials(path: &Path, values: &Credentials, exclusive: bool) -> Result<()> {
    let mut text = String::new();
    for (key, value) in values {
        if key.contains('=') || key.contains('\n') || value.contains('\n') {
            bail!("CSF credential contains a newline or invalid key");
        }
        text.push_str(key);
        text.push('=');
        text.push_str(value);
        text.push('\n');
    }
    write_private_text(path, &text, exclusive)
}

fn ensure_agent_mcp_key(state_dir: &Path) -> Result<PathBuf> {
    ensure_private_directory(state_dir)?;
    let path = state_dir.join("agent-mcp-key");
    if !path.exists() {
        let token = random_token(32)?;
        match write_private_text(&path, &(token + "\n"), true) {
            Ok(()) => {}
            Err(error) if path.exists() => {
                let _ = error;
            }
            Err(error) => return Err(error),
        }
    }
    read_private_text(&path, "agent MCP key", false)?;
    Ok(path)
}

fn read_agent_mcp_key(state_dir: &Path) -> Result<String> {
    let path = ensure_agent_mcp_key(state_dir)?;
    Ok(read_private_text(&path, "agent MCP key", false)?
        .trim()
        .to_owned())
}

fn ensure_copilot_token(state_dir: &Path) -> Result<(PathBuf, String, bool)> {
    let path = state_dir.join("copilot-token");
    let token = COPILOT_TOKEN_ENVIRONMENTS
        .iter()
        .find_map(|name| {
            std::env::var(name)
                .ok()
                .filter(|value| !value.trim().is_empty())
        })
        .unwrap_or_default()
        .trim()
        .to_owned();
    let existing = if path.exists() {
        read_private_text(&path, "Copilot token", true)?
            .trim()
            .to_owned()
    } else {
        String::new()
    };
    if !token.is_empty() {
        validate_private_value(&token, "Copilot token", false)?;
        let changed = existing != token;
        if changed {
            write_private_text(&path, &(token.clone() + "\n"), false)?;
        }
        return Ok((path, token, changed));
    }
    if !path.exists() {
        write_private_text(&path, "", true)?;
    }
    Ok((path, existing, false))
}

fn ensure_private_directory(path: &Path) -> Result<()> {
    if let Ok(metadata) = fs::symlink_metadata(path) {
        if metadata.file_type().is_symlink() || !metadata.is_dir() {
            bail!("CSF private state directory must not be a symlink or non-directory.");
        }
    } else {
        fs::create_dir_all(path)?;
    }
    #[cfg(unix)]
    fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    Ok(())
}

fn read_private_text(path: &Path, description: &str, allow_empty: bool) -> Result<String> {
    let metadata = fs::symlink_metadata(path).with_context(|| {
        format!("CSF {description} must be a regular file in the private state directory.")
    })?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        bail!("CSF {description} must be a regular file in the private state directory.");
    }
    #[cfg(unix)]
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    let text = fs::read_to_string(path)?;
    validate_private_value(text.trim(), description, allow_empty)?;
    Ok(text)
}

fn validate_private_value(value: &str, description: &str, allow_empty: bool) -> Result<()> {
    if value.len() > 4096
        || (!allow_empty && value.is_empty())
        || value
            .chars()
            .any(|character| character.is_whitespace() || character.is_control())
    {
        bail!("CSF {description} file is empty or malformed.");
    }
    Ok(())
}

fn write_private_text(path: &Path, value: &str, exclusive: bool) -> Result<()> {
    if path
        .symlink_metadata()
        .is_ok_and(|metadata| metadata.file_type().is_symlink())
    {
        bail!("CSF private credential path must not be a symlink.");
    }
    if let Some(parent) = path.parent() {
        ensure_private_directory(parent)?;
    }
    let mut options = OpenOptions::new();
    options.write(true).create(true);
    if exclusive {
        options.create_new(true);
    } else {
        options.truncate(true);
    }
    #[cfg(unix)]
    options.mode(0o600);
    let mut file = options.open(path)?;
    file.write_all(value.as_bytes())?;
    #[cfg(unix)]
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    Ok(())
}

fn write_json(path: &Path, value: &Value, private: bool) -> Result<()> {
    let mut text = serde_json::to_string_pretty(value)?;
    text.push('\n');
    if private {
        write_private_text(path, &text, false)
    } else {
        if let Some(parent) = path.parent() {
            fs::create_dir_all(parent)?;
        }
        fs::write(path, text)?;
        Ok(())
    }
}

fn redact(text: &str, values: &Credentials) -> String {
    let mut redacted = text.to_owned();
    for (key, value) in values {
        if !value.is_empty()
            && ["PASSWORD", "SECRET", "AUTH", "SALT", "KEY"]
                .iter()
                .any(|word| key.contains(word))
        {
            redacted = redacted.replace(value, "[REDACTED]");
        }
    }
    if let (Some(public), Some(secret)) = (
        values.get("LANGFUSE_PUBLIC_KEY"),
        values.get("LANGFUSE_SECRET_KEY"),
    ) {
        let authorization =
            base64::engine::general_purpose::STANDARD.encode(format!("{public}:{secret}"));
        redacted = redacted.replace(&format!("Basic {authorization}"), "[REDACTED]");
    }
    redacted
}

fn ensure_process_success(output: &ProcessOutput, description: &str) -> Result<()> {
    if output.success {
        return Ok(());
    }
    bail!(
        "{description} failed with exit {}.",
        output
            .code
            .map_or_else(|| "unknown".to_owned(), |code| code.to_string())
    )
}

fn validate_remote_url(remote: &str) -> Result<()> {
    if remote.is_empty() {
        return Ok(());
    }
    if let Ok(parsed) = Url::parse(remote) {
        let permitted_ssh_user = parsed.scheme() == "ssh" && parsed.username() == "git";
        if parsed.password().is_some()
            || parsed.query().is_some()
            || parsed.fragment().is_some()
            || (!parsed.username().is_empty() && !permitted_ssh_user)
        {
            bail!("CSF source origin URL embeds credentials; configure Git credentials outside the URL before running `candace csf up`.");
        }
        return Ok(());
    }
    if let Some((user_host, _)) = remote.split_once(':') {
        if let Some((user, _)) = user_host.split_once('@') {
            if user != "git" {
                bail!("CSF source origin URL embeds credentials; configure Git credentials outside the URL before running `candace csf up`.");
            }
        }
    }
    Ok(())
}

fn discover_source_root() -> Result<PathBuf> {
    let mut candidates = VecDeque::new();
    if let Some(configured) = std::env::var_os("CANDACE_CSF_ROOT") {
        candidates.push_back(PathBuf::from(configured));
    }
    if let Ok(current) = std::env::current_dir() {
        candidates.extend(current.ancestors().map(Path::to_path_buf));
    }
    if let Ok(executable) = std::env::current_exe().and_then(fs::canonicalize) {
        if let Some(parent) = executable.parent() {
            candidates.extend(parent.ancestors().map(Path::to_path_buf));
        }
    }
    let manifest = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
    candidates.extend(manifest.ancestors().map(Path::to_path_buf));

    let mut seen = BTreeSet::new();
    while let Some(root) = candidates.pop_front() {
        for candidate in [root.clone(), root.join("candace"), root.join("csf")] {
            let candidate = absolute_path(&candidate)?;
            if !seen.insert(candidate.clone()) {
                continue;
            }
            if candidate.join("go.mod").is_file()
                && candidate.join("app/csf/cmd/main.go").is_file()
                && candidate.join("infra/docker-compose.yaml").is_file()
            {
                return Ok(candidate);
            }
        }
    }
    bail!("CSF source was not found beside this operator checkout.")
}

fn state_dir() -> Result<PathBuf> {
    if let Some(configured) = std::env::var_os("CANDACE_CSF_STATE_DIR") {
        return absolute_path(Path::new(&configured));
    }
    let home =
        std::env::var_os("HOME").context("HOME is required for the default CSF state path")?;
    Ok(PathBuf::from(home).join(".local/state/csf"))
}

fn absolute_path(path: &Path) -> Result<PathBuf> {
    if path.is_absolute() {
        Ok(path.to_path_buf())
    } else {
        Ok(std::env::current_dir()?.join(path))
    }
}

fn required_string<'a>(value: &'a Value, key: &str) -> Result<&'a str> {
    value
        .get(key)
        .and_then(Value::as_str)
        .with_context(|| format!("JSON response omitted {key}"))
}

fn random_bytes(length: usize) -> Result<Vec<u8>> {
    let mut bytes = vec![0_u8; length];
    getrandom::fill(&mut bytes)
        .map_err(|error| anyhow!("operating-system randomness failed: {error}"))?;
    Ok(bytes)
}

fn random_hex(length: usize) -> Result<String> {
    Ok(random_bytes(length)?
        .into_iter()
        .map(|byte| format!("{byte:02x}"))
        .collect())
}

fn random_token(length: usize) -> Result<String> {
    Ok(base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(random_bytes(length)?))
}

#[cfg(unix)]
fn effective_uid() -> u32 {
    unsafe { libc::geteuid() }
}

#[cfg(unix)]
fn effective_gid() -> u32 {
    unsafe { libc::getegid() }
}

#[cfg(not(unix))]
fn effective_uid() -> u32 {
    0
}

#[cfg(not(unix))]
fn effective_gid() -> u32 {
    0
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;
    use tempfile::TempDir;

    struct FakeRunner {
        captures: Mutex<VecDeque<ProcessOutput>>,
        invocations: Mutex<Vec<Invocation>>,
        stdin_invocations: Mutex<Vec<Invocation>>,
    }

    impl FakeRunner {
        fn with_captures(captures: Vec<ProcessOutput>) -> Self {
            Self {
                captures: Mutex::new(captures.into()),
                invocations: Mutex::new(Vec::new()),
                stdin_invocations: Mutex::new(Vec::new()),
            }
        }
    }

    impl ProcessRunner for FakeRunner {
        fn capture(&self, invocation: &Invocation) -> Result<ProcessOutput> {
            self.invocations.lock().unwrap().push(invocation.clone());
            self.captures
                .lock()
                .unwrap()
                .pop_front()
                .context("unexpected process capture")
        }

        fn stream(&self, invocation: &Invocation, _values: &Credentials) -> Result<()> {
            self.invocations.lock().unwrap().push(invocation.clone());
            Ok(())
        }

        fn stream_with_stdin(&self, invocation: &Invocation, _values: &Credentials) -> Result<()> {
            self.stdin_invocations
                .lock()
                .unwrap()
                .push(invocation.clone());
            Ok(())
        }
    }

    fn success(stdout: &str) -> ProcessOutput {
        ProcessOutput {
            success: true,
            code: Some(0),
            stdout: stdout.to_owned(),
            stderr: String::new(),
        }
    }

    fn fixture_operator(runner: FakeRunner, temporary: &TempDir) -> Operator<FakeRunner> {
        let source = temporary.path().join("source");
        fs::create_dir_all(source.join("infra")).unwrap();
        fs::write(source.join("infra/docker-compose.yaml"), "name: csf\n").unwrap();
        Operator::new(runner, source, temporary.path().join("state")).unwrap()
    }

    #[test]
    fn candace_csf_defaults_to_dry_up() {
        let parsed = Cli::try_parse_from(["candace", "csf"]).unwrap();
        let CommandGroup::Csf { action } = parsed.command;
        assert_eq!(
            action.unwrap_or(Action::Up { dry: true }),
            Action::Up { dry: true }
        );
    }

    #[test]
    fn explicit_up_is_live_and_dry_is_opt_in() {
        let parsed = Cli::try_parse_from(["candace", "csf", "up"]).unwrap();
        let CommandGroup::Csf { action } = parsed.command;
        assert_eq!(action, Some(Action::Up { dry: false }));
        let parsed = Cli::try_parse_from(["candace", "csf", "up", "--dry"]).unwrap();
        let CommandGroup::Csf { action } = parsed.command;
        assert_eq!(action, Some(Action::Up { dry: true }));
    }

    #[test]
    fn dry_up_does_not_call_a_process_runner_or_create_state() {
        let temporary = TempDir::new().unwrap();
        let operator = fixture_operator(FakeRunner::with_captures(vec![]), &temporary);
        fs::create_dir_all(operator.source_root.join("app/csf")).unwrap();
        fs::write(
            operator.source_root.join("app/csf/Dockerfile"),
            "FROM scratch\n",
        )
        .unwrap();
        fs::write(
            operator.infra_root.join("docker-compose.yaml"),
            "name: csf\nservices:\n  runtime:\n    image: example.invalid/csf\n",
        )
        .unwrap();
        fs::write(operator.infra_root.join("mcp-tools.json"), "{\"tools\":[]}").unwrap();
        operator.dry_up().unwrap();
        assert!(operator.runner.invocations.lock().unwrap().is_empty());
        assert!(!operator.state_dir.exists());
    }

    #[test]
    #[cfg(unix)]
    fn dry_up_reads_a_retained_port_without_rewriting_private_state() {
        let temporary = TempDir::new().unwrap();
        let operator = fixture_operator(FakeRunner::with_captures(vec![]), &temporary);
        fs::create_dir_all(operator.source_root.join("app/csf")).unwrap();
        fs::write(
            operator.source_root.join("app/csf/Dockerfile"),
            "FROM scratch\n",
        )
        .unwrap();
        fs::write(
            operator.infra_root.join("docker-compose.yaml"),
            "name: csf\nservices:\n  runtime:\n    image: example.invalid/csf\n",
        )
        .unwrap();
        fs::write(operator.infra_root.join("mcp-tools.json"), "{\"tools\":[]}").unwrap();
        fs::create_dir_all(&operator.state_dir).unwrap();
        let credentials = operator.state_dir.join("credentials.env");
        fs::write(
            &credentials,
            "CSF_RUNTIME_ADDRESS=127.0.0.1\nCSF_RUNTIME_PORT=14112\n",
        )
        .unwrap();
        fs::set_permissions(&credentials, fs::Permissions::from_mode(0o640)).unwrap();
        operator.dry_up().unwrap();
        assert!(operator.runner.invocations.lock().unwrap().is_empty());
        assert_eq!(
            fs::metadata(&credentials).unwrap().permissions().mode() & 0o777,
            0o640
        );
    }

    #[test]
    fn runtime_recreation_is_only_requested_when_the_token_changes() {
        assert_eq!(
            runtime_up_args(false),
            ["up", "-d", "--wait", "--wait-timeout", "300", "runtime"]
        );
        assert_eq!(
            runtime_up_args(true),
            [
                "up",
                "-d",
                "--force-recreate",
                "--wait",
                "--wait-timeout",
                "300",
                "runtime"
            ]
        );
    }

    #[test]
    fn call_forwards_stdin_to_the_inner_csf_command() {
        let temporary = TempDir::new().unwrap();
        let runner = FakeRunner::with_captures(vec![]);
        let operator = fixture_operator(runner, &temporary);
        fs::create_dir_all(&operator.state_dir).unwrap();
        fs::write(
            operator.state_dir.join("credentials.env"),
            "CSF_RUNTIME_PORT=14111\n",
        )
        .unwrap();

        operator.call("Search").unwrap();

        let invocations = operator.runner.stdin_invocations.lock().unwrap();
        assert_eq!(invocations.len(), 1);
        let args: Vec<_> = invocations[0]
            .args
            .iter()
            .map(|value| value.to_string_lossy().into_owned())
            .collect();
        assert!(args.windows(2).any(|pair| pair == ["exec", "-T"]));
        assert_eq!(args.last().map(String::as_str), Some("Search"));
    }

    #[test]
    fn retained_runtime_port_survives_live_provisioning_configuration() {
        let temporary = TempDir::new().unwrap();
        let operator = fixture_operator(FakeRunner::with_captures(vec![]), &temporary);
        fs::create_dir_all(&operator.state_dir).unwrap();
        let mut values = Credentials::from([
            ("CSF_DB_PASSWORD".to_owned(), "db-password".to_owned()),
            ("CSF_RUNTIME_ADDRESS".to_owned(), "127.0.0.1".to_owned()),
            ("CSF_RUNTIME_PORT".to_owned(), "14112".to_owned()),
        ]);

        let _ = operator.private_runtime_config(&mut values).unwrap();

        assert_eq!(values["CSF_RUNTIME_PORT"], "14112");
        assert_eq!(values["CSF_RUNTIME_ADDRESS"], "127.0.0.1");
    }

    #[test]
    fn agent_key_is_private_idempotent_and_opaque() {
        let temporary = TempDir::new().unwrap();
        let state = temporary.path().join("state");
        let first = read_agent_mcp_key(&state).unwrap();
        let second = read_agent_mcp_key(&state).unwrap();
        assert_eq!(first, second);
        assert!(first.len() >= 40);
        #[cfg(unix)]
        assert_eq!(
            fs::metadata(state.join("agent-mcp-key"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
    }

    #[test]
    #[cfg(unix)]
    fn agent_key_refuses_a_symlink() {
        use std::os::unix::fs::symlink;

        let temporary = TempDir::new().unwrap();
        let state = temporary.path().join("state");
        fs::create_dir(&state).unwrap();
        let target = temporary.path().join("other-secret");
        fs::write(&target, "do-not-print\n").unwrap();
        symlink(&target, state.join("agent-mcp-key")).unwrap();
        let error = read_agent_mcp_key(&state).unwrap_err().to_string();
        assert!(!error.contains("do-not-print"));
        assert!(error.contains("regular file"));
    }

    #[test]
    fn credentials_refuse_orphaned_compose_volumes() {
        let temporary = TempDir::new().unwrap();
        let operator = fixture_operator(
            FakeRunner::with_captures(vec![success("csf_postgres\n")]),
            &temporary,
        );
        let error = operator.credentials(true).unwrap_err().to_string();
        assert!(error.contains("volumes exist without their original credentials"));
        assert!(!operator.state_dir.join("credentials.env").exists());
    }

    #[test]
    fn compose_invocation_owns_one_named_project_and_definition() {
        let temporary = TempDir::new().unwrap();
        let operator = fixture_operator(FakeRunner::with_captures(vec![]), &temporary);
        let invocation = operator.compose_invocation(&["ps", "--all"]);
        let args: Vec<_> = invocation
            .args
            .iter()
            .map(|value| value.to_string_lossy().into_owned())
            .collect();
        assert_eq!(invocation.program, "docker");
        assert_eq!(args[0], "compose");
        assert!(args.windows(2).any(|pair| pair == ["-p", "csf"]));
        assert!(args
            .iter()
            .any(|arg| arg.ends_with("infra/docker-compose.yaml")));
    }

    #[test]
    fn redaction_covers_values_and_basic_authorization() {
        let values = Credentials::from([
            (
                "POSTGRES_PASSWORD".to_owned(),
                "private-password".to_owned(),
            ),
            ("LANGFUSE_PUBLIC_KEY".to_owned(), "pk-private".to_owned()),
            ("LANGFUSE_SECRET_KEY".to_owned(), "sk-private".to_owned()),
        ]);
        let authorization =
            base64::engine::general_purpose::STANDARD.encode("pk-private:sk-private");
        let output = redact(
            &format!("private-password Basic {authorization} sk-private"),
            &values,
        );
        assert!(!output.contains("private-password"));
        assert!(!output.contains("sk-private"));
        assert!(!output.contains(&authorization));
    }

    #[test]
    fn credential_creation_uses_private_permissions_and_csf_names() {
        let temporary = TempDir::new().unwrap();
        let operator = fixture_operator(FakeRunner::with_captures(vec![success("")]), &temporary);
        let values = operator.credentials(true).unwrap();
        assert_eq!(values["CSF_LANGFUSE_PORT"], "14300");
        assert_eq!(values["CSF_OPENSEARCH_PORT"], "19200");
        #[cfg(unix)]
        assert_eq!(
            fs::metadata(operator.state_dir.join("credentials.env"))
                .unwrap()
                .permissions()
                .mode()
                & 0o777,
            0o600
        );
    }

    #[test]
    fn remote_credentials_are_rejected_before_clone() {
        assert!(validate_remote_url("https://user:token@example.invalid/csf.git").is_err());
        assert!(validate_remote_url("ssh://git@example.invalid/csf.git").is_ok());
        assert!(validate_remote_url("git@example.invalid:csf.git").is_ok());
        assert!(validate_remote_url("person@example.invalid:csf.git").is_err());
    }
}
