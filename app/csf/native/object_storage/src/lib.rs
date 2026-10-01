use std::collections::BTreeMap;
use std::error::Error;
use std::fs::{self, File};
use std::io::{self, IsTerminal, Write};
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::Duration;

use aws_config::retry::RetryConfig;
use aws_config::timeout::TimeoutConfig;
use aws_credential_types::provider::error::CredentialsError;
use aws_credential_types::Credentials;
use aws_sdk_s3::error::SdkError;
use aws_sdk_s3::operation::head_bucket::HeadBucketError;
use aws_sdk_s3::Client as S3Client;
use http::StatusCode;
use thiserror::Error;

pub const EXIT_CONFIGURATION: i32 = 2;
const EXIT_FALLBACK_AVAILABLE: i32 = 10;
const EXIT_AWS_UNAVAILABLE: i32 = 20;
const AWS_OPERATION_TIMEOUT: Duration = Duration::from_secs(20);
const AWS_CONNECT_TIMEOUT: Duration = Duration::from_secs(5);
const AWS_READ_TIMEOUT: Duration = Duration::from_secs(10);
const AWS_PREFLIGHT_TIMEOUT: Duration = Duration::from_secs(20);
const AWS_MAX_ATTEMPTS: u32 = 2;
pub const DEFAULT_LANGFUSE_ENV: &str = "/etc/candace/csf/langfuse.env";
pub const DEFAULT_MINIO_ENV: &str = "/etc/candace/csf/minio.env";
pub const BINARY_NAME: &str = "object-storage";
const CREDENTIAL_PROVIDER_NAME: &str = "candace-csf-object-storage-preflight";
const EMPTY_CONFIGURATION: &str = "CHANGE_ME";
const DEFAULT_MINIO_BUCKET: &str = "langfuse";
const DEFAULT_MINIO_REGION: &str = "auto";
const LOCAL_S3_ENDPOINT: &str = "http://127.0.0.1:19000";
const LOCAL_S3_CONSOLE_ENDPOINT: &str = "http://127.0.0.1:19001";
const LOCAL_S3_DATA_PATH: &str = "/var/lib/candace-csf-minio";
const MINIO_COMPONENT_BINARY: &str = "/opt/candace/csf/components/minio/bin/minio";
const MINIO_CLIENT_BINARY: &str = "/opt/candace/csf/components/minio/bin/mc";
const MINIO_UNIT_FILE: &str = "/usr/lib/systemd/system/candace-csf-minio.service";
const MINIO_SERVICE: &str = "candace-csf-minio.service";
const LANGFUSE_WEB_SERVICE: &str = "candace-csf-langfuse-web.service";
const LANGFUSE_WORKER_SERVICE: &str = "candace-csf-langfuse-worker.service";
const LANGFUSE_ENV_MODE: u32 = 0o600;
const MINIO_ROOT_USER_KEY: &str = "MINIO_ROOT_USER";
const MINIO_ROOT_PASSWORD_KEY: &str = "MINIO_ROOT_PASSWORD";
const MINIO_BUCKET_KEY: &str = "MINIO_LANGFUSE_BUCKET";
const AWS_ACCESS_KEY_ID_KEY: &str = "AWS_ACCESS_KEY_ID";
const AWS_SECRET_ACCESS_KEY_KEY: &str = "AWS_SECRET_ACCESS_KEY";
const AWS_SESSION_TOKEN_KEY: &str = "AWS_SESSION_TOKEN";
const AWS_PROFILE_KEY: &str = "AWS_PROFILE";
const AWS_CONFIG_FILE_KEY: &str = "AWS_CONFIG_FILE";
const AWS_SHARED_CREDENTIALS_FILE_KEY: &str = "AWS_SHARED_CREDENTIALS_FILE";
const AWS_WEB_IDENTITY_TOKEN_FILE_KEY: &str = "AWS_WEB_IDENTITY_TOKEN_FILE";
const AWS_ROLE_ARN_KEY: &str = "AWS_ROLE_ARN";
const AWS_ROLE_SESSION_NAME_KEY: &str = "AWS_ROLE_SESSION_NAME";
const AWS_CONTAINER_CREDENTIALS_RELATIVE_URI_KEY: &str = "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI";
const AWS_CONTAINER_CREDENTIALS_FULL_URI_KEY: &str = "AWS_CONTAINER_CREDENTIALS_FULL_URI";
const AWS_CONTAINER_AUTHORIZATION_TOKEN_KEY: &str = "AWS_CONTAINER_AUTHORIZATION_TOKEN";
const AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE_KEY: &str = "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE";
const AWS_ENDPOINT_URL_KEY: &str = "AWS_ENDPOINT_URL";
const AWS_ENDPOINT_URL_S3_KEY: &str = "AWS_ENDPOINT_URL_S3";
const AWS_REGION_KEY: &str = "AWS_REGION";
const AWS_DEFAULT_REGION_KEY: &str = "AWS_DEFAULT_REGION";
const AWS_EC2_METADATA_DISABLED_KEY: &str = "AWS_EC2_METADATA_DISABLED";
const AWS_EC2_METADATA_SERVICE_ENDPOINT_KEY: &str = "AWS_EC2_METADATA_SERVICE_ENDPOINT";
const AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE_KEY: &str = "AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE";
const AWS_USE_FIPS_ENDPOINT_KEY: &str = "AWS_USE_FIPS_ENDPOINT";
const AWS_USE_DUALSTACK_ENDPOINT_KEY: &str = "AWS_USE_DUALSTACK_ENDPOINT";
const EVENT_BUCKET_KEY: &str = "LANGFUSE_S3_EVENT_UPLOAD_BUCKET";
const EVENT_REGION_KEY: &str = "LANGFUSE_S3_EVENT_UPLOAD_REGION";
const EVENT_ACCESS_KEY: &str = "LANGFUSE_S3_EVENT_UPLOAD_ACCESS_KEY_ID";
const EVENT_SECRET_KEY: &str = "LANGFUSE_S3_EVENT_UPLOAD_SECRET_ACCESS_KEY";
const EVENT_ENDPOINT_KEY: &str = "LANGFUSE_S3_EVENT_UPLOAD_ENDPOINT";
const EVENT_PATH_STYLE_KEY: &str = "LANGFUSE_S3_EVENT_UPLOAD_FORCE_PATH_STYLE";
const MEDIA_BUCKET_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_BUCKET";
const MEDIA_REGION_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_REGION";
const MEDIA_ACCESS_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_ACCESS_KEY_ID";
const MEDIA_SECRET_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_SECRET_ACCESS_KEY";
const MEDIA_ENDPOINT_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_ENDPOINT";
const MEDIA_PATH_STYLE_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_FORCE_PATH_STYLE";
const MEDIA_INTERNAL_ENDPOINT_KEY: &str = "LANGFUSE_S3_MEDIA_UPLOAD_INTERNAL_ENDPOINT";
const SYSTEMCTL_EXECUTABLE: &str = "systemctl";
const USE_MINIO_COMMAND: &str = "use-minio";
const CONFIRMATION_FLAG: &str = "--yes";
const FALLBACK_CONFIG_FLAG: &str = "--langfuse-env";
const FALLBACK_MINIO_CONFIG_FLAG: &str = "--minio-env";
const SYSTEMCTL_ENABLE_NOW: [&str; 2] = ["enable", "--now"];
const SYSTEMCTL_TRY_RESTART: &str = "try-restart";
const YES_ANSWER: &str = "yes";
const SHORT_YES_ANSWER: &str = "y";
const CONFIG_TRUE: &str = "true";
const CONFIG_FALSE: &str = "false";
const UNSUPPORTED_AWS_CONFIGURATION_KEYS: [&str; 19] = [
    AWS_PROFILE_KEY,
    AWS_CONFIG_FILE_KEY,
    AWS_SHARED_CREDENTIALS_FILE_KEY,
    AWS_WEB_IDENTITY_TOKEN_FILE_KEY,
    AWS_ROLE_ARN_KEY,
    AWS_ROLE_SESSION_NAME_KEY,
    AWS_CONTAINER_CREDENTIALS_RELATIVE_URI_KEY,
    AWS_CONTAINER_CREDENTIALS_FULL_URI_KEY,
    AWS_CONTAINER_AUTHORIZATION_TOKEN_KEY,
    AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE_KEY,
    AWS_ENDPOINT_URL_KEY,
    AWS_ENDPOINT_URL_S3_KEY,
    AWS_REGION_KEY,
    AWS_DEFAULT_REGION_KEY,
    AWS_EC2_METADATA_DISABLED_KEY,
    AWS_EC2_METADATA_SERVICE_ENDPOINT_KEY,
    AWS_EC2_METADATA_SERVICE_ENDPOINT_MODE_KEY,
    AWS_USE_FIPS_ENDPOINT_KEY,
    AWS_USE_DUALSTACK_ENDPOINT_KEY,
];

#[derive(Clone, Copy)]
struct S3ConfigurationKeys {
    label: &'static str,
    bucket: &'static str,
    region: &'static str,
    access_key: &'static str,
    secret_key: &'static str,
    endpoint: &'static str,
    path_style: &'static str,
}

const S3_CONFIGURATIONS: [S3ConfigurationKeys; 2] = [
    S3ConfigurationKeys {
        label: "event upload",
        bucket: EVENT_BUCKET_KEY,
        region: EVENT_REGION_KEY,
        access_key: EVENT_ACCESS_KEY,
        secret_key: EVENT_SECRET_KEY,
        endpoint: EVENT_ENDPOINT_KEY,
        path_style: EVENT_PATH_STYLE_KEY,
    },
    S3ConfigurationKeys {
        label: "media upload",
        bucket: MEDIA_BUCKET_KEY,
        region: MEDIA_REGION_KEY,
        access_key: MEDIA_ACCESS_KEY,
        secret_key: MEDIA_SECRET_KEY,
        endpoint: MEDIA_ENDPOINT_KEY,
        path_style: MEDIA_PATH_STYLE_KEY,
    },
];

#[derive(Debug, Error)]
pub enum ConfigurationError {
    #[error("cannot read {path}")]
    Read { path: PathBuf },
    #[error("invalid environment assignment in {path} at line {line}")]
    InvalidAssignment { path: PathBuf, line: usize },
    #[error("invalid shell-style environment value in {path} at line {line}")]
    InvalidValue { path: PathBuf, line: usize },
    #[error("invalid boolean value for {key}")]
    InvalidBoolean { key: &'static str },
    #[error("configure {bucket_key} and {region_key} before preflight")]
    MissingBucketOrRegion {
        bucket_key: &'static str,
        region_key: &'static str,
    },
    #[error("configure both {access_key} and {secret_key}")]
    IncompleteCredentials {
        access_key: &'static str,
        secret_key: &'static str,
    },
    #[error("configure {session_token} only with its AWS access-key pair")]
    SessionTokenWithoutCredentials { session_token: &'static str },
    #[error("unsupported AWS service credential or endpoint setting {key}")]
    UnsupportedAwsConfiguration { key: &'static str },
}

#[derive(Clone, Eq, PartialEq)]
pub struct S3Configuration {
    pub label: &'static str,
    pub bucket: String,
    pub region: String,
    access_key: Option<String>,
    secret_key: Option<String>,
    session_token: Option<String>,
    pub endpoint: Option<String>,
    pub force_path_style: bool,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AwsErrorKind {
    Auth,
    MissingBucket,
    WrongRegion,
    Transient,
    Unknown,
    Setup,
}

impl AwsErrorKind {
    fn permits_minio(self) -> bool {
        matches!(self, Self::Auth | Self::MissingBucket)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct AwsFailure {
    pub kind: AwsErrorKind,
    detail: &'static str,
}

impl AwsFailure {
    pub fn safe_detail(&self) -> &str {
        self.detail
    }
}

#[derive(Debug)]
pub struct PreflightOptions {
    pub non_interactive: bool,
    pub langfuse_env: PathBuf,
    pub minio_env: PathBuf,
    pub program: PathBuf,
}

pub fn read_environment(path: &Path) -> Result<BTreeMap<String, String>, ConfigurationError> {
    let content = fs::read_to_string(path).map_err(|_| ConfigurationError::Read {
        path: path.to_path_buf(),
    })?;
    let mut values = BTreeMap::new();
    for (index, line) in content.lines().enumerate() {
        let line_number = index + 1;
        let stripped = line.trim();
        if stripped.is_empty() || stripped.starts_with('#') {
            continue;
        }
        let Some((key, value)) = stripped.split_once('=') else {
            return Err(ConfigurationError::InvalidAssignment {
                path: path.to_path_buf(),
                line: line_number,
            });
        };
        let key = key.trim();
        if !valid_environment_key(key) {
            return Err(ConfigurationError::InvalidAssignment {
                path: path.to_path_buf(),
                line: line_number,
            });
        }
        let parsed = decode_environment_value(value.trim()).ok_or_else(|| {
            ConfigurationError::InvalidValue {
                path: path.to_path_buf(),
                line: line_number,
            }
        })?;
        values.insert(key.to_owned(), parsed);
    }
    Ok(values)
}

fn valid_environment_key(key: &str) -> bool {
    let mut chars = key.chars();
    matches!(chars.next(), Some(first) if first == '_' || first.is_ascii_alphabetic())
        && chars.all(|character| character == '_' || character.is_ascii_alphanumeric())
}

fn decode_environment_value(value: &str) -> Option<String> {
    if !matches!(value.chars().next(), Some('"' | '\'')) {
        // systemd interprets backslashes in unquoted values; reject them instead of
        // preflighting a value different from the one passed to the service.
        if value.contains('\\') {
            return None;
        }
        return Some(value.to_owned());
    }
    let parts = shlex::split(value)?;
    if parts.len() != 1 {
        return None;
    }
    let parsed = parts.into_iter().next()?;
    let parsed_hashes = parsed.chars().filter(|character| *character == '#').count();
    let source_hashes = value.chars().filter(|character| *character == '#').count();
    (parsed_hashes == source_hashes).then_some(parsed)
}

pub fn s3_configurations(
    values: &BTreeMap<String, String>,
) -> Result<Vec<S3Configuration>, ConfigurationError> {
    for key in UNSUPPORTED_AWS_CONFIGURATION_KEYS {
        if nonempty(values.get(key)).is_some() {
            return Err(ConfigurationError::UnsupportedAwsConfiguration { key });
        }
    }
    S3_CONFIGURATIONS
        .into_iter()
        .map(|keys| {
            let bucket = values.get(keys.bucket).cloned().unwrap_or_default();
            let region = values.get(keys.region).cloned().unwrap_or_default();
            if bucket.is_empty() || bucket == EMPTY_CONFIGURATION || region.is_empty() {
                return Err(ConfigurationError::MissingBucketOrRegion {
                    bucket_key: keys.bucket,
                    region_key: keys.region,
                });
            }
            let langfuse_access_key = nonempty(values.get(keys.access_key));
            let langfuse_secret_key = nonempty(values.get(keys.secret_key));
            if langfuse_access_key.is_some() != langfuse_secret_key.is_some() {
                return Err(ConfigurationError::IncompleteCredentials {
                    access_key: keys.access_key,
                    secret_key: keys.secret_key,
                });
            }
            let (access_key, secret_key, session_token) =
                if let (Some(access_key), Some(secret_key)) =
                    (langfuse_access_key, langfuse_secret_key)
                {
                    (Some(access_key), Some(secret_key), None)
                } else {
                    let access_key = nonempty(values.get(AWS_ACCESS_KEY_ID_KEY));
                    let secret_key = nonempty(values.get(AWS_SECRET_ACCESS_KEY_KEY));
                    let session_token = nonempty(values.get(AWS_SESSION_TOKEN_KEY));
                    if access_key.is_some() != secret_key.is_some() {
                        return Err(ConfigurationError::IncompleteCredentials {
                            access_key: AWS_ACCESS_KEY_ID_KEY,
                            secret_key: AWS_SECRET_ACCESS_KEY_KEY,
                        });
                    }
                    if session_token.is_some() && access_key.is_none() {
                        return Err(ConfigurationError::SessionTokenWithoutCredentials {
                            session_token: AWS_SESSION_TOKEN_KEY,
                        });
                    }
                    (access_key, secret_key, session_token)
                };
            let force_path_style = match values.get(keys.path_style).map(String::as_str) {
                None | Some("") | Some(CONFIG_FALSE) => false,
                Some(CONFIG_TRUE) => true,
                _ => {
                    return Err(ConfigurationError::InvalidBoolean {
                        key: keys.path_style,
                    })
                }
            };
            Ok(S3Configuration {
                label: keys.label,
                bucket,
                region,
                access_key,
                secret_key,
                session_token,
                endpoint: nonempty(values.get(keys.endpoint)),
                force_path_style,
            })
        })
        .collect()
}

fn nonempty(value: Option<&String>) -> Option<String> {
    value.filter(|value| !value.is_empty()).cloned()
}

fn classify_status(status: Option<StatusCode>, modeled_missing_bucket: bool) -> AwsFailure {
    if modeled_missing_bucket || status == Some(StatusCode::NOT_FOUND) {
        return AwsFailure {
            kind: AwsErrorKind::MissingBucket,
            detail: "configured S3 bucket was not found",
        };
    }
    if matches!(
        status,
        Some(StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN)
    ) {
        return AwsFailure {
            kind: AwsErrorKind::Auth,
            detail: "configured identity was rejected by the S3 service",
        };
    }
    if matches!(
        status,
        Some(StatusCode::MOVED_PERMANENTLY | StatusCode::TEMPORARY_REDIRECT)
    ) {
        return AwsFailure {
            kind: AwsErrorKind::WrongRegion,
            detail: "configured bucket region does not match the S3 service response",
        };
    }
    if status
        .is_some_and(|status| status.is_server_error() || status == StatusCode::TOO_MANY_REQUESTS)
    {
        return AwsFailure {
            kind: AwsErrorKind::Transient,
            detail: "S3 service is temporarily unavailable",
        };
    }
    AwsFailure {
        kind: AwsErrorKind::Unknown,
        detail: "S3 preflight failed without a recognized authorization result",
    }
}

fn has_missing_credentials(error: &(dyn Error + 'static)) -> bool {
    let mut source = Some(error);
    while let Some(current) = source {
        if matches!(
            current.downcast_ref::<CredentialsError>(),
            Some(CredentialsError::CredentialsNotLoaded(_))
        ) {
            return true;
        }
        source = current.source();
    }
    false
}

fn classify_head_bucket_error(error: &SdkError<HeadBucketError>) -> AwsFailure {
    let status = error
        .raw_response()
        .and_then(|response| StatusCode::from_u16(response.status().as_u16()).ok());
    let modeled_missing_bucket = error
        .as_service_error()
        .is_some_and(HeadBucketError::is_not_found);
    let classified = classify_status(status, modeled_missing_bucket);
    if classified.kind != AwsErrorKind::Unknown {
        return classified;
    }
    if has_missing_credentials(error) {
        return AwsFailure {
            kind: AwsErrorKind::Auth,
            detail: "no AWS credentials were available",
        };
    }
    match error {
        SdkError::TimeoutError(_) | SdkError::DispatchFailure(_) => AwsFailure {
            kind: AwsErrorKind::Transient,
            detail: "S3 request timed out or could not reach the service",
        },
        _ => classified,
    }
}

// Keep loading asynchronous so SDK credential providers stay inside the same runtime as requests.
async fn aws_shared_config_async(configuration: &S3Configuration) -> aws_config::SdkConfig {
    let timeout = TimeoutConfig::builder()
        .operation_timeout(AWS_OPERATION_TIMEOUT)
        .connect_timeout(AWS_CONNECT_TIMEOUT)
        .read_timeout(AWS_READ_TIMEOUT)
        .build();
    #[allow(deprecated)]
    let isolated_profiles = aws_config::profile::profile_file::ProfileFiles::builder()
        .with_contents(
            aws_config::profile::profile_file::ProfileFileKind::Config,
            "",
        )
        .with_contents(
            aws_config::profile::profile_file::ProfileFileKind::Credentials,
            "",
        )
        .build();
    // The invoking user's AWS environment and ~/.aws files are not the service's
    // credentials. Empty SDK inputs leave only the service-file static pair or IMDS.
    let mut loader = aws_config::defaults(aws_config::BehaviorVersion::latest())
        .empty_test_environment()
        .profile_files(isolated_profiles)
        .region(aws_config::Region::new(configuration.region.clone()))
        .timeout_config(timeout)
        .retry_config(RetryConfig::standard().with_max_attempts(AWS_MAX_ATTEMPTS));
    if let (Some(access_key), Some(secret_key)) =
        (&configuration.access_key, &configuration.secret_key)
    {
        loader = loader.credentials_provider(Credentials::new(
            access_key,
            secret_key,
            configuration.session_token.clone(),
            None,
            CREDENTIAL_PROVIDER_NAME,
        ));
    }
    loader.load().await
}

async fn check_s3_configurations(
    configurations: &[S3Configuration],
) -> Result<Vec<String>, AwsFailure> {
    let mut passed = Vec::new();
    for configuration in configurations {
        let shared_config = aws_shared_config_async(configuration).await;
        let mut s3_builder = aws_sdk_s3::config::Builder::from(&shared_config)
            .force_path_style(configuration.force_path_style);
        if let Some(endpoint) = &configuration.endpoint {
            s3_builder = s3_builder.endpoint_url(endpoint);
        }
        let s3 = S3Client::from_conf(s3_builder.build());
        if let Err(error) = s3.head_bucket().bucket(&configuration.bucket).send().await {
            return Err(classify_head_bucket_error(&error));
        }
        passed.push(format!(
            "S3 HeadBucket passed for Langfuse {}: s3://{} in {}",
            configuration.label, configuration.bucket, configuration.region
        ));
    }
    Ok(passed)
}

async fn within_preflight_timeout<T>(
    budget: Duration,
    operation: impl std::future::Future<Output = Result<T, AwsFailure>>,
) -> Result<T, AwsFailure> {
    match tokio::time::timeout(budget, operation).await {
        Ok(result) => result,
        Err(_) => Err(AwsFailure {
            kind: AwsErrorKind::Transient,
            detail: "S3 preflight exceeded its time budget",
        }),
    }
}

pub fn run_preflight(options: PreflightOptions) -> i32 {
    let configurations = match read_environment(&options.langfuse_env)
        .and_then(|values| s3_configurations(&values))
    {
        Ok(configurations) => configurations,
        Err(error) => {
            eprintln!("Cannot read Langfuse object-store configuration: {error}");
            return EXIT_CONFIGURATION;
        }
    };
    match tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
    {
        Ok(runtime) => match runtime.block_on(within_preflight_timeout(
            AWS_PREFLIGHT_TIMEOUT,
            check_s3_configurations(&configurations),
        )) {
            Ok(passed) => {
                for message in passed {
                    println!("{message}");
                }
                println!("HeadBucket confirms bucket existence and access; it does not prove PutObject permission.");
                0
            }
            Err(failure) => preflight_failure(&options, failure),
        },
        Err(_) => {
            eprintln!("Cannot initialize AWS SDK runtime");
            EXIT_CONFIGURATION
        }
    }
}

fn preflight_failure(options: &PreflightOptions, failure: AwsFailure) -> i32 {
    let kind = failure.kind;
    let message = match kind {
        AwsErrorKind::Auth => "AWS credentials cannot access the configured S3 bucket",
        AwsErrorKind::MissingBucket => {
            "The configured S3 bucket does not exist or is not visible to this identity"
        }
        AwsErrorKind::WrongRegion => "The configured S3 bucket is in another region",
        AwsErrorKind::Transient => "AWS S3 is temporarily unavailable",
        AwsErrorKind::Setup => "AWS SDK setup failed",
        AwsErrorKind::Unknown => "S3 preflight failed without a recognized authorization result",
    };
    eprintln!("{message}: {}", failure.safe_detail());
    if !kind.permits_minio() {
        eprintln!("Local fallback was not offered because this is not a credential, permission, or missing-bucket result.");
        return if kind == AwsErrorKind::Setup {
            EXIT_CONFIGURATION
        } else {
            EXIT_AWS_UNAVAILABLE
        };
    }
    print_local_plan(&options.langfuse_env, &options.minio_env, &mut io::stderr());
    let command = fallback_command(&options.program, &options.langfuse_env, &options.minio_env);
    eprintln!("Optional local object storage is available only through this explicit command:");
    eprintln!("{command}");
    if options.non_interactive || !io::stdin().is_terminal() {
        return EXIT_FALLBACK_AVAILABLE;
    }
    eprint!("Start and enable the optional local object-store unit, update Langfuse, and restart active Langfuse dependencies? [y/N] ");
    let _ = io::stderr().flush();
    let mut answer = String::new();
    if io::stdin().read_line(&mut answer).is_err()
        || ![SHORT_YES_ANSWER, YES_ANSWER]
            .iter()
            .any(|yes| answer.trim().eq_ignore_ascii_case(yes))
    {
        return EXIT_FALLBACK_AVAILABLE;
    }
    if unsafe { libc::geteuid() } != 0 {
        eprintln!("Run the displayed sudo command to apply the accepted plan.");
        return EXIT_FALLBACK_AVAILABLE;
    }
    match use_minio(true, &options.langfuse_env, &options.minio_env, None, None) {
        Ok(()) => 0,
        Err(error) => {
            eprintln!("object storage setup failed: {error}");
            EXIT_CONFIGURATION
        }
    }
}

fn fallback_command(program: &Path, langfuse_env: &Path, minio_env: &Path) -> String {
    [
        "sudo".to_owned(),
        program.display().to_string(),
        USE_MINIO_COMMAND.to_owned(),
        CONFIRMATION_FLAG.to_owned(),
        FALLBACK_CONFIG_FLAG.to_owned(),
        langfuse_env.display().to_string(),
        FALLBACK_MINIO_CONFIG_FLAG.to_owned(),
        minio_env.display().to_string(),
    ]
    .into_iter()
    .map(|argument| {
        shlex::try_quote(&argument)
            .map(|quoted| quoted.into_owned())
            .unwrap_or_default()
    })
    .collect::<Vec<_>>()
    .join(" ")
}

fn local_bucket(minio_env: &Path) -> String {
    read_environment(minio_env)
        .ok()
        .and_then(|values| values.get(MINIO_BUCKET_KEY).cloned())
        .unwrap_or_else(|| {
            format!("{DEFAULT_MINIO_BUCKET} (configure {MINIO_BUCKET_KEY} in {DEFAULT_MINIO_ENV})")
        })
}

fn print_local_plan(langfuse_env: &Path, minio_env: &Path, output: &mut impl Write) {
    let bucket = local_bucket(minio_env);
    let _ = writeln!(output, "Explicit local fallback plan:");
    let _ = writeln!(output, "- Run the pinned MinIO server and mc client as one independently supervised native systemd unit.");
    let _ = writeln!(
        output,
        "- Bind the S3 API to {LOCAL_S3_ENDPOINT} and console to {LOCAL_S3_CONSOLE_ENDPOINT}."
    );
    let _ = writeln!(output, "- Persist object data under {LOCAL_S3_DATA_PATH}.");
    let _ = writeln!(output, "- Create the local bucket {bucket}.");
    let _ = writeln!(output, "- Read operator-owned {MINIO_ROOT_USER_KEY} and {MINIO_ROOT_PASSWORD_KEY} values without displaying them.");
    let _ = writeln!(
        output,
        "- Rewrite {} atomically for the local endpoint and credentials.",
        langfuse_env.display()
    );
    let _ = writeln!(
        output,
        "- Try-restart only currently active Langfuse web and worker units."
    );
}

#[derive(Debug, Error)]
pub enum SetupError {
    #[error("Refusing host changes without --yes")]
    ConfirmationRequired,
    #[error("use-minio must run as root")]
    MustRunAsRoot,
    #[error("the optional local object-store component is not installed: {paths}; build and install an archive that explicitly selects the minio component")]
    MinioNotInstalled { paths: String },
    #[error("cannot read object-store configuration")]
    ReadConfiguration,
    #[error("configure local object-store credentials before enabling it")]
    MissingMinioCredentials,
    #[error("cannot safely update Langfuse environment")]
    UpdateEnvironment,
    #[error("systemctl {operation} failed")]
    Systemctl { operation: String },
}

pub fn update_environment(
    content: &str,
    replacements: &BTreeMap<String, String>,
) -> Result<String, SetupError> {
    let mut pending: BTreeMap<_, _> = replacements
        .iter()
        .map(|(key, value)| (key.as_str(), value.as_str()))
        .collect();
    let mut result = Vec::new();
    for line in content.lines() {
        let key = if !line.trim_start().starts_with('#') {
            line.split_once('=')
                .map(|(key, _)| key.trim())
                .unwrap_or("")
        } else {
            ""
        };
        if replacements.contains_key(key) {
            if let Some(value) = pending.remove(key) {
                result.push(format!("{key}={}", environment_quote(value)?));
            }
        } else {
            result.push(line.to_owned());
        }
    }
    for (key, value) in pending {
        result.push(format!("{key}={}", environment_quote(value)?));
    }
    Ok(format!("{}\n", result.join("\n")))
}

fn environment_quote(value: &str) -> Result<String, SetupError> {
    if value
        .chars()
        .any(|character| matches!(character, '\0' | '\n' | '\r'))
    {
        return Err(SetupError::UpdateEnvironment);
    }
    let escaped = value.replace('\\', "\\\\").replace('"', "\\\"");
    Ok(format!("\"{escaped}\""))
}

fn minio_replacements(user: &str, password: &str, bucket: &str) -> BTreeMap<String, String> {
    let mut replacements = BTreeMap::new();
    for keys in S3_CONFIGURATIONS {
        replacements.insert(keys.bucket.to_owned(), bucket.to_owned());
        replacements.insert(keys.region.to_owned(), DEFAULT_MINIO_REGION.to_owned());
        replacements.insert(keys.access_key.to_owned(), user.to_owned());
        replacements.insert(keys.secret_key.to_owned(), password.to_owned());
        replacements.insert(keys.endpoint.to_owned(), LOCAL_S3_ENDPOINT.to_owned());
        replacements.insert(keys.path_style.to_owned(), CONFIG_TRUE.to_owned());
    }
    replacements.insert(
        MEDIA_INTERNAL_ENDPOINT_KEY.to_owned(),
        LOCAL_S3_ENDPOINT.to_owned(),
    );
    replacements
}

pub fn use_minio(
    yes: bool,
    langfuse_env: &Path,
    minio_env: &Path,
    effective_uid: Option<u32>,
    required_paths: Option<&[PathBuf]>,
) -> Result<(), SetupError> {
    use_minio_with(
        yes,
        langfuse_env,
        minio_env,
        effective_uid,
        required_paths,
        systemctl,
    )
}

pub fn use_minio_with(
    yes: bool,
    langfuse_env: &Path,
    minio_env: &Path,
    effective_uid: Option<u32>,
    required_paths: Option<&[PathBuf]>,
    mut systemctl_runner: impl FnMut(&[&str]) -> Result<(), SetupError>,
) -> Result<(), SetupError> {
    if !yes {
        return Err(SetupError::ConfirmationRequired);
    }
    if effective_uid.unwrap_or_else(|| unsafe { libc::geteuid() }) != 0 {
        return Err(SetupError::MustRunAsRoot);
    }
    let default_required = [
        PathBuf::from(MINIO_COMPONENT_BINARY),
        PathBuf::from(MINIO_CLIENT_BINARY),
        PathBuf::from(MINIO_UNIT_FILE),
    ];
    let required_paths = required_paths.unwrap_or(&default_required);
    let missing = required_paths
        .iter()
        .filter(|path| !path.exists())
        .map(|path| path.display().to_string())
        .collect::<Vec<_>>();
    if !missing.is_empty() {
        return Err(SetupError::MinioNotInstalled {
            paths: missing.join(", "),
        });
    }
    let values = read_environment(minio_env).map_err(|_| SetupError::ReadConfiguration)?;
    let current = fs::read_to_string(langfuse_env).map_err(|_| SetupError::ReadConfiguration)?;
    let user = values
        .get(MINIO_ROOT_USER_KEY)
        .map(String::as_str)
        .unwrap_or("");
    let password = values
        .get(MINIO_ROOT_PASSWORD_KEY)
        .map(String::as_str)
        .unwrap_or("");
    let bucket = values
        .get(MINIO_BUCKET_KEY)
        .map(String::as_str)
        .unwrap_or(DEFAULT_MINIO_BUCKET);
    if user.is_empty()
        || password.is_empty()
        || user == EMPTY_CONFIGURATION
        || password == EMPTY_CONFIGURATION
    {
        return Err(SetupError::MissingMinioCredentials);
    }
    let content = update_environment(&current, &minio_replacements(user, password, bucket))?;
    let parent = langfuse_env.parent().ok_or(SetupError::UpdateEnvironment)?;
    let mut pending =
        tempfile::NamedTempFile::new_in(parent).map_err(|_| SetupError::UpdateEnvironment)?;
    pending
        .write_all(content.as_bytes())
        .and_then(|()| pending.as_file().sync_all())
        .map_err(|_| SetupError::UpdateEnvironment)?;
    pending
        .as_file()
        .set_permissions(fs::Permissions::from_mode(LANGFUSE_ENV_MODE))
        .map_err(|_| SetupError::UpdateEnvironment)?;
    systemctl_runner(&[
        SYSTEMCTL_ENABLE_NOW[0],
        SYSTEMCTL_ENABLE_NOW[1],
        MINIO_SERVICE,
    ])?;
    pending
        .persist(langfuse_env)
        .map_err(|_| SetupError::UpdateEnvironment)?;
    File::open(parent)
        .and_then(|directory| directory.sync_all())
        .map_err(|_| SetupError::UpdateEnvironment)?;
    systemctl_runner(&[
        SYSTEMCTL_TRY_RESTART,
        LANGFUSE_WEB_SERVICE,
        LANGFUSE_WORKER_SERVICE,
    ])?;
    Ok(())
}

fn systemctl(arguments: &[&str]) -> Result<(), SetupError> {
    let status = Command::new(SYSTEMCTL_EXECUTABLE)
        .args(arguments)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .map_err(|_| SetupError::Systemctl {
            operation: arguments.join(" "),
        })?;
    if status.success() {
        Ok(())
    } else {
        Err(SetupError::Systemctl {
            operation: arguments.join(" "),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use aws_smithy_runtime_api::client::orchestrator::HttpResponse;
    use aws_smithy_runtime_api::client::result::SdkError;
    use aws_smithy_runtime_api::http::StatusCode as SmithyStatusCode;
    use aws_smithy_types::body::SdkBody;
    use std::os::unix::fs::PermissionsExt;

    fn sdk_response(status: StatusCode) -> HttpResponse {
        HttpResponse::new(
            SmithyStatusCode::try_from(status.as_u16()).unwrap(),
            SdkBody::empty(),
        )
    }

    fn read_temp_environment(content: &str) -> BTreeMap<String, String> {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("langfuse.env");
        fs::write(&path, content).unwrap();
        read_environment(&path).unwrap()
    }

    fn valid_values() -> BTreeMap<String, String> {
        read_temp_environment(
            "LANGFUSE_S3_EVENT_UPLOAD_BUCKET=events\n\
             LANGFUSE_S3_EVENT_UPLOAD_REGION=us-east-1\n\
             LANGFUSE_S3_EVENT_UPLOAD_ACCESS_KEY_ID=event-key\n\
             LANGFUSE_S3_EVENT_UPLOAD_SECRET_ACCESS_KEY=event-secret\n\
             LANGFUSE_S3_MEDIA_UPLOAD_BUCKET=media\n\
             LANGFUSE_S3_MEDIA_UPLOAD_REGION=us-west-2\n",
        )
    }

    #[test]
    fn systemd_environment_values_preserve_whitespace_hashes_and_duplicates() {
        let values = read_temp_environment(
            "# comment\nTOKEN=first\nTOKEN=\"quoted value # retained\"\nPASSWORD=#literal\nINLINE=prefix#inline\nSPACED=unquoted interior whitespace\nEMPTY=\n",
        );
        assert_eq!(values["TOKEN"], "quoted value # retained");
        assert_eq!(values["PASSWORD"], "#literal");
        assert_eq!(values["INLINE"], "prefix#inline");
        assert_eq!(values["SPACED"], "unquoted interior whitespace");
        assert_eq!(values["EMPTY"], "");
    }

    #[test]
    fn malformed_environment_diagnostic_does_not_reveal_assignment_contents() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("credentials.env");
        fs::write(&path, "PRIVATE_CREDENTIAL_WITHOUT_ASSIGNMENT\n").unwrap();
        let error = read_environment(&path).unwrap_err().to_string();
        assert!(error.contains("line 1"));
        assert!(!error.contains("PRIVATE_CREDENTIAL"));
    }

    #[test]
    fn malformed_or_multiword_values_are_rejected() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("langfuse.env");
        for value in ["'unterminated", "\"unterminated"] {
            fs::write(&path, format!("TOKEN={value}\n")).unwrap();
            assert!(matches!(
                read_environment(&path),
                Err(ConfigurationError::InvalidValue { .. })
            ));
        }
        fs::write(&path, "TOKEN=secret #literal\n").unwrap();
        assert_eq!(read_environment(&path).unwrap()["TOKEN"], "secret #literal");
        fs::write(&path, "TOKEN=secret\\ value\n").unwrap();
        assert!(matches!(
            read_environment(&path),
            Err(ConfigurationError::InvalidValue { .. })
        ));
    }

    #[test]
    fn s3_configuration_requires_buckets_and_complete_identity_pairs() {
        let mut values = valid_values();
        values.remove(MEDIA_BUCKET_KEY);
        assert!(matches!(
            s3_configurations(&values),
            Err(ConfigurationError::MissingBucketOrRegion { .. })
        ));
        let mut values = valid_values();
        values.remove(EVENT_SECRET_KEY);
        assert!(matches!(
            s3_configurations(&values),
            Err(ConfigurationError::IncompleteCredentials { .. })
        ));
    }

    #[test]
    fn service_aws_credentials_include_only_the_matching_session_token() {
        let mut values = valid_values();
        values.remove(EVENT_ACCESS_KEY);
        values.remove(EVENT_SECRET_KEY);
        values.insert(AWS_ACCESS_KEY_ID_KEY.to_owned(), "service-key".to_owned());
        values.insert(
            AWS_SECRET_ACCESS_KEY_KEY.to_owned(),
            "service-secret".to_owned(),
        );
        values.insert(AWS_SESSION_TOKEN_KEY.to_owned(), "service-token".to_owned());
        let configurations = s3_configurations(&values).unwrap();
        assert_eq!(configurations[0].access_key.as_deref(), Some("service-key"));
        assert_eq!(
            configurations[0].secret_key.as_deref(),
            Some("service-secret")
        );
        assert_eq!(
            configurations[0].session_token.as_deref(),
            Some("service-token")
        );
        assert_eq!(
            configurations[1].session_token.as_deref(),
            Some("service-token")
        );
    }

    #[test]
    fn per_storage_credentials_do_not_mix_with_service_aws_session_token() {
        let mut values = valid_values();
        values.insert(AWS_ACCESS_KEY_ID_KEY.to_owned(), "other-key".to_owned());
        values.insert(
            AWS_SECRET_ACCESS_KEY_KEY.to_owned(),
            "other-secret".to_owned(),
        );
        values.insert(AWS_SESSION_TOKEN_KEY.to_owned(), "other-token".to_owned());
        let configurations = s3_configurations(&values).unwrap();
        assert_eq!(configurations[0].access_key.as_deref(), Some("event-key"));
        assert_eq!(configurations[0].session_token, None);
        assert_eq!(
            configurations[1].session_token.as_deref(),
            Some("other-token")
        );
    }

    #[test]
    fn unsupported_service_profile_and_credential_sources_are_rejected() {
        for key in UNSUPPORTED_AWS_CONFIGURATION_KEYS {
            let mut values = valid_values();
            values.insert(key.to_owned(), "configured".to_owned());
            assert!(matches!(
                s3_configurations(&values),
                Err(ConfigurationError::UnsupportedAwsConfiguration {
                    key: rejected_key
                }) if rejected_key == key
            ));
        }
    }

    #[tokio::test]
    async fn sdk_configuration_ignores_invoking_user_endpoint_and_region() {
        const AWS_ENDPOINT_URL: &str = "AWS_ENDPOINT_URL";
        const AWS_REGION: &str = "AWS_REGION";
        const CHILD_ENV: &str = "CSF_TEST_SDK_ENV_CHILD";
        if std::env::var_os(CHILD_ENV).is_none() {
            // Set environment at process creation, before any runtime threads
            // exist; mutating this test process would race parallel SDK users.
            let output = Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "tests::sdk_configuration_ignores_invoking_user_endpoint_and_region",
                ])
                .env(CHILD_ENV, "1")
                .env(AWS_ENDPOINT_URL, LOCAL_S3_ENDPOINT)
                .env(AWS_REGION, "wrong-region")
                .output()
                .unwrap();
            assert!(
                output.status.success(),
                "{}\n{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            return;
        }
        let configuration = s3_configurations(&valid_values()).unwrap().remove(0);
        let sdk_config = aws_shared_config_async(&configuration).await;
        assert_eq!(
            sdk_config.region().map(|region| region.as_ref()),
            Some("us-east-1")
        );
        assert_eq!(sdk_config.endpoint_url(), None);
    }

    #[test]
    fn custom_endpoint_and_path_style_are_retained() {
        let mut values = valid_values();
        values.insert(EVENT_ENDPOINT_KEY.to_owned(), LOCAL_S3_ENDPOINT.to_owned());
        values.insert(EVENT_PATH_STYLE_KEY.to_owned(), "true".to_owned());
        let configurations = s3_configurations(&values).unwrap();
        assert_eq!(
            configurations[0].endpoint.as_deref(),
            Some(LOCAL_S3_ENDPOINT)
        );
        assert!(configurations[0].force_path_style);
        assert!(configurations[1].endpoint.is_none());
        assert!(!configurations[1].force_path_style);
        assert_eq!(configurations[0].access_key.as_deref(), Some("event-key"));
    }

    #[test]
    fn only_typed_auth_and_missing_bucket_results_offer_minio() {
        let cases = [
            (
                Some(StatusCode::UNAUTHORIZED),
                false,
                AwsErrorKind::Auth,
                true,
            ),
            (Some(StatusCode::FORBIDDEN), false, AwsErrorKind::Auth, true),
            (
                Some(StatusCode::NOT_FOUND),
                false,
                AwsErrorKind::MissingBucket,
                true,
            ),
            (None, true, AwsErrorKind::MissingBucket, true),
            (
                Some(StatusCode::MOVED_PERMANENTLY),
                false,
                AwsErrorKind::WrongRegion,
                false,
            ),
            (
                Some(StatusCode::TOO_MANY_REQUESTS),
                false,
                AwsErrorKind::Transient,
                false,
            ),
            (
                Some(StatusCode::SERVICE_UNAVAILABLE),
                false,
                AwsErrorKind::Transient,
                false,
            ),
            (
                Some(StatusCode::BAD_REQUEST),
                false,
                AwsErrorKind::Unknown,
                false,
            ),
            (None, false, AwsErrorKind::Unknown, false),
        ];
        for (status, missing_bucket, expected, permits_minio) in cases {
            let failure = classify_status(status, missing_bucket);
            assert_eq!(failure.kind, expected);
            assert_eq!(failure.kind.permits_minio(), permits_minio);
            if let Some(status) = status {
                let error = SdkError::response_error("S3 response", sdk_response(status));
                assert_eq!(classify_head_bucket_error(&error), failure);
            }
        }
    }

    #[test]
    fn sdk_modeled_head_bucket_not_found_is_missing_bucket() {
        let not_found = aws_sdk_s3::types::error::NotFound::builder().build();
        let bucket_error = SdkError::service_error(
            HeadBucketError::NotFound(not_found),
            sdk_response(StatusCode::BAD_REQUEST),
        );
        assert_eq!(
            classify_head_bucket_error(&bucket_error).kind,
            AwsErrorKind::MissingBucket
        );
    }

    #[tokio::test]
    async fn stalled_credential_or_network_work_times_out_without_minio_offer() {
        let failure = within_preflight_timeout(
            Duration::from_millis(1),
            std::future::pending::<Result<(), AwsFailure>>(),
        )
        .await
        .unwrap_err();
        assert_eq!(failure.kind, AwsErrorKind::Transient);
        assert!(!failure.kind.permits_minio());
    }

    #[test]
    fn update_collapses_replaced_duplicates_and_quotes_values() {
        let content = "LANGFUSE_S3_EVENT_UPLOAD_BUCKET=remote-events\n\
                       LANGFUSE_S3_EVENT_UPLOAD_BUCKET=duplicate-remote-events\n\
                       LANGFUSE_S3_MEDIA_UPLOAD_BUCKET=remote-media\n\
                       UNCHANGED=value\n";
        let replacements = BTreeMap::from([
            (EVENT_BUCKET_KEY.to_owned(), "local bucket".to_owned()),
            ("Z_LAST".to_owned(), "last".to_owned()),
            ("A_FIRST".to_owned(), "first".to_owned()),
        ]);
        let updated = update_environment(content, &replacements).unwrap();
        assert_eq!(
            updated.matches("LANGFUSE_S3_EVENT_UPLOAD_BUCKET=").count(),
            1
        );
        assert!(updated.contains("LANGFUSE_S3_EVENT_UPLOAD_BUCKET=\"local bucket\""));
        assert!(updated.contains("LANGFUSE_S3_MEDIA_UPLOAD_BUCKET=remote-media"));
        assert!(updated.contains("UNCHANGED=value"));
        assert!(updated.ends_with("A_FIRST=\"first\"\nZ_LAST=\"last\"\n"));
    }

    #[test]
    fn environment_quote_round_trips_systemd_double_quote_values() {
        let value = "apostrophe' double\" backslash\\ space #literal";
        let content = update_environment(
            "UNCHANGED=value\n",
            &BTreeMap::from([("ROUND_TRIP".to_owned(), value.to_owned())]),
        )
        .unwrap();
        assert_eq!(read_temp_environment(&content)["ROUND_TRIP"], value);
        assert!(update_environment(
            "",
            &BTreeMap::from([("BAD".to_owned(), "line\nbreak".to_owned(),)])
        )
        .is_err());
    }

    #[test]
    fn explicit_minio_switch_is_atomic_mode_600_and_touches_only_its_units() {
        let directory = tempfile::tempdir().unwrap();
        let langfuse_env = directory.path().join("langfuse.env");
        let minio_env = directory.path().join("minio.env");
        fs::write(
            &langfuse_env,
            "LANGFUSE_S3_EVENT_UPLOAD_BUCKET=remote\nUNCHANGED=value\n",
        )
        .unwrap();
        fs::write(&minio_env, "MINIO_ROOT_USER=local-user\nMINIO_ROOT_PASSWORD='local secret'\nMINIO_LANGFUSE_BUCKET=local-langfuse\n").unwrap();
        let mut calls = Vec::new();
        use_minio_with(
            true,
            &langfuse_env,
            &minio_env,
            Some(0),
            Some(&[]),
            |arguments| {
                calls.push(
                    arguments
                        .iter()
                        .map(|argument| (*argument).to_owned())
                        .collect::<Vec<_>>(),
                );
                Ok(())
            },
        )
        .unwrap();
        assert_eq!(
            calls,
            [
                ["enable", "--now", MINIO_SERVICE],
                ["try-restart", LANGFUSE_WEB_SERVICE, LANGFUSE_WORKER_SERVICE],
            ]
        );
        let content = fs::read_to_string(&langfuse_env).unwrap();
        assert!(content.contains("LANGFUSE_S3_EVENT_UPLOAD_BUCKET=\"local-langfuse\""));
        assert!(content.contains(&format!("{MEDIA_ENDPOINT_KEY}=\"{LOCAL_S3_ENDPOINT}\"")));
        assert!(content.contains(&format!("{MEDIA_PATH_STYLE_KEY}=\"{CONFIG_TRUE}\"")));
        assert!(content.contains("LANGFUSE_S3_MEDIA_UPLOAD_SECRET_ACCESS_KEY=\"local secret\""));
        assert!(content.contains("UNCHANGED=value"));
        assert_eq!(
            fs::metadata(&langfuse_env).unwrap().permissions().mode() & 0o777,
            LANGFUSE_ENV_MODE
        );
    }

    #[test]
    fn minio_requires_confirmation_root_credentials_and_installed_component_before_systemd() {
        let directory = tempfile::tempdir().unwrap();
        let langfuse_env = directory.path().join("langfuse.env");
        let minio_env = directory.path().join("minio.env");
        fs::write(&langfuse_env, "UNCHANGED=value\n").unwrap();
        fs::write(
            &minio_env,
            "MINIO_ROOT_USER=user\nMINIO_ROOT_PASSWORD=password\n",
        )
        .unwrap();
        let mut calls = Vec::new();
        assert!(matches!(
            use_minio_with(false, &langfuse_env, &minio_env, Some(0), Some(&[]), |_| {
                calls.push("systemctl");
                Ok(())
            }),
            Err(SetupError::ConfirmationRequired)
        ));
        assert!(matches!(
            use_minio_with(
                true,
                &langfuse_env,
                &minio_env,
                Some(1000),
                Some(&[]),
                |_| {
                    calls.push("systemctl");
                    Ok(())
                }
            ),
            Err(SetupError::MustRunAsRoot)
        ));
        assert!(matches!(
            use_minio_with(
                true,
                &langfuse_env,
                &minio_env,
                Some(0),
                Some(&[PathBuf::from("/missing")]),
                |_| {
                    calls.push("systemctl");
                    Ok(())
                }
            ),
            Err(SetupError::MinioNotInstalled { .. })
        ));
        assert!(calls.is_empty());
        assert_eq!(
            fs::read_to_string(langfuse_env).unwrap(),
            "UNCHANGED=value\n"
        );
    }
}
