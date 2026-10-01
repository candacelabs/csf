use std::path::PathBuf;
use std::process::ExitCode;

use candace_csf_object_storage::{
    run_preflight, use_minio, PreflightOptions, BINARY_NAME, DEFAULT_LANGFUSE_ENV,
    DEFAULT_MINIO_ENV, EXIT_CONFIGURATION,
};
use clap::{Parser, Subcommand};

#[derive(Debug, Parser)]
#[command(about = "Preflight Langfuse AWS S3 and explicitly enable local object storage")]
struct Arguments {
    #[command(subcommand)]
    command: Command,
}

#[derive(Debug, Subcommand)]
enum Command {
    /// Check the configured AWS identity and Langfuse event/media buckets.
    Preflight {
        #[arg(long)]
        non_interactive: bool,
        #[arg(long, default_value = DEFAULT_LANGFUSE_ENV)]
        langfuse_env: PathBuf,
        #[arg(long, default_value = DEFAULT_MINIO_ENV)]
        minio_env: PathBuf,
    },
    /// Explicitly enable the installed loopback MinIO fallback and use it in Langfuse.
    UseMinio {
        #[arg(long)]
        yes: bool,
        #[arg(long, default_value = DEFAULT_LANGFUSE_ENV)]
        langfuse_env: PathBuf,
        #[arg(long, default_value = DEFAULT_MINIO_ENV)]
        minio_env: PathBuf,
    },
}

fn main() -> ExitCode {
    let arguments = Arguments::parse();
    let code = match arguments.command {
        Command::Preflight {
            non_interactive,
            langfuse_env,
            minio_env,
        } => run_preflight(PreflightOptions {
            non_interactive,
            langfuse_env,
            minio_env,
            program: std::env::current_exe().unwrap_or_else(|_| PathBuf::from(BINARY_NAME)),
        }),
        Command::UseMinio {
            yes,
            langfuse_env,
            minio_env,
        } => match use_minio(yes, &langfuse_env, &minio_env, None, None) {
            Ok(()) => 0,
            Err(error) => {
                eprintln!("object storage setup failed: {error}");
                EXIT_CONFIGURATION
            }
        },
    };
    ExitCode::from(code as u8)
}
