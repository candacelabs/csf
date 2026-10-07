// Copyright 2026 Candace Labs

//! `csf-docker-proxy`: one Unix socket per session in front of the Docker
//! Engine.
//!
//! The session's `DOCKER_HOST` points at this socket. The proxy parses each
//! request's head, decides with [`csf_sandbox::dockerfilter`] whether it is on
//! the allow-list, and for a container operation confirms the target carries
//! this session's label before forwarding. A forwarded request is spliced to a
//! fresh upstream connection with [`tokio::io::copy_bidirectional`], so the
//! hijacked attach stream `docker run` relies on — and therefore
//! `tools/bazel.sh` — works through the proxy unchanged. Anything off the
//! allow-list, or any container not labelled with this session, gets a `403`
//! and never reaches the Engine.

use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::Arc;

use anyhow::{bail, Context, Result};
use clap::Parser;
use serde_json::Value;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::{UnixListener, UnixStream};

use csf_sandbox::dockerfilter::{self, ContainerOp, CreateLimits, Request};

#[derive(Parser)]
#[command(name = "csf-docker-proxy", about = "Filtering Docker proxy for one harness session")]
struct Args {
    /// The Unix socket to listen on; the session's DOCKER_HOST points here.
    #[arg(long)]
    socket: PathBuf,
    /// The real Docker Engine socket to forward allowed calls to.
    #[arg(long, default_value = "/var/run/docker.sock")]
    upstream: PathBuf,
    /// The session this proxy serves; every container it creates is labelled
    /// with it and every container operation is checked against it.
    #[arg(long)]
    session: String,
    /// An image reference the session may run; repeat for several. The harness
    /// passes the digests the repository pins.
    #[arg(long = "allow-image")]
    allow_images: Vec<String>,
    /// A path a bind mount's source must lie within; repeat for several.
    #[arg(long = "session-path")]
    session_paths: Vec<PathBuf>,
}

struct Config {
    upstream: PathBuf,
    session: String,
    allow_images: Vec<String>,
    session_paths: Vec<PathBuf>,
}

#[tokio::main]
async fn main() -> Result<()> {
    let args = Args::parse();
    // A stale socket from a crashed predecessor would block the bind.
    let _ = std::fs::remove_file(&args.socket);
    let listener = UnixListener::bind(&args.socket)
        .with_context(|| format!("listen on {}", args.socket.display()))?;
    std::fs::set_permissions(&args.socket, std::fs::Permissions::from_mode(0o600))
        .with_context(|| format!("restrict {}", args.socket.display()))?;
    let config = Arc::new(Config {
        upstream: args.upstream,
        session: args.session,
        allow_images: args.allow_images,
        session_paths: args.session_paths,
    });
    eprintln!("csf-docker-proxy: session {} listening on {}", config.session, args.socket.display());
    loop {
        let (client, _) = listener.accept().await.context("accept")?;
        let config = config.clone();
        tokio::spawn(async move {
            if let Err(error) = serve(client, config).await {
                eprintln!("csf-docker-proxy: {error:#}");
            }
        });
    }
}

/// The parsed head of one request: enough to decide and to forward.
struct Head {
    method: String,
    path: String,
    headers: Vec<(String, String)>,
    content_length: usize,
    /// The exact request-head bytes, through the blank line, to forward as-is.
    raw: Vec<u8>,
    /// Body bytes already read past the head.
    leftover: Vec<u8>,
}

// Each connection carries exactly one request. A forwarded non-upgrade request
// goes upstream with `Connection: close`, so the Engine closes after responding
// and the Docker client opens a fresh connection — which this proxy classifies
// again — for its next request. Without that, HTTP keep-alive would let a second
// request ride a connection whose first request was allowed, past the filter.
async fn serve(mut client: UnixStream, config: Arc<Config>) -> Result<()> {
    let head = match read_head(&mut client).await? {
        Some(head) => head,
        None => return Ok(()), // client closed before sending a request
    };
    let request = match dockerfilter::classify(&head.method, &head.path) {
        Ok(request) => request,
        Err(reason) => return deny(&mut client, &reason).await,
    };
    match request {
        Request::Ping | Request::Version | Request::ImageInspect => {
            relay(client, &config.upstream, &head, None).await
        }
        Request::ContainerCreate => serve_create(client, config, head).await,
        Request::ContainerOp { id, kind } => serve_container_op(client, config, head, id, kind).await,
    }
}

/// Forwards a create only after the body passes the create checks, and with the
/// session label injected so the container is attributable.
async fn serve_create(mut client: UnixStream, config: Arc<Config>, head: Head) -> Result<()> {
    let body = read_body(&mut client, &head).await?;
    let decoded: Value = match serde_json::from_slice(&body) {
        Ok(value) => value,
        Err(error) => return deny(&mut client, &format!("refused: create body is not JSON: {error}")).await,
    };
    let limits = CreateLimits { allowed_images: &config.allow_images, session_paths: &config.session_paths };
    if let Err(reason) = dockerfilter::check_create(&decoded, &limits) {
        return deny(&mut client, &reason).await;
    }
    let labelled = dockerfilter::with_session_label(decoded, &config.session);
    let new_body = serde_json::to_vec(&labelled).context("re-encode the create body")?;
    relay(client, &config.upstream, &head, Some(new_body)).await
}

/// Forwards a container operation only after confirming the container carries
/// this session's label. An attach hijacks the connection, so it is spliced in
/// both directions; every other operation is a one-shot forward.
async fn serve_container_op(
    mut client: UnixStream,
    config: Arc<Config>,
    head: Head,
    id: String,
    kind: ContainerOp,
) -> Result<()> {
    let belongs = match inspect(&config.upstream, &id).await? {
        Some(inspect) => dockerfilter::container_belongs(&inspect, &config.session),
        None => false,
    };
    if !belongs {
        return deny(&mut client, &format!("refused: container {id} is not labelled with this session")).await;
    }
    if kind == ContainerOp::Attach {
        return splice_upgrade(client, &config.upstream, &head).await;
    }
    relay(client, &config.upstream, &head, None).await
}

/// Forwards one non-upgrade request and relays its response. The request goes
/// upstream with `Connection: close` (and, for a rewritten body, the new
/// length), and the response is streamed back until the Engine closes the
/// connection, which bounds the relay without parsing the response framing.
async fn relay(mut client: UnixStream, upstream: &std::path::Path, head: &Head, body: Option<Vec<u8>>) -> Result<()> {
    let mut up = UnixStream::connect(upstream)
        .await
        .with_context(|| format!("connect upstream {}", upstream.display()))?;
    let request_head = rebuild_request(head, body.as_ref().map(Vec::len));
    up.write_all(&request_head).await.context("write head upstream")?;
    let payload = body.unwrap_or_else(|| head.leftover.clone());
    if !payload.is_empty() {
        up.write_all(&payload).await.context("write body upstream")?;
    }
    up.flush().await.ok();
    tokio::io::copy(&mut up, &mut client).await.map(|_| ()).or_else(ignore_disconnect)?;
    client.shutdown().await.ok();
    Ok(())
}

/// Forwards an upgrade request (a container attach) unchanged and splices both
/// directions, so the hijacked stream `docker run` relies on flows until either
/// side closes.
async fn splice_upgrade(mut client: UnixStream, upstream: &std::path::Path, head: &Head) -> Result<()> {
    let mut up = UnixStream::connect(upstream)
        .await
        .with_context(|| format!("connect upstream {}", upstream.display()))?;
    up.write_all(&head.raw).await.context("write head upstream")?;
    if !head.leftover.is_empty() {
        up.write_all(&head.leftover).await.context("write body upstream")?;
    }
    up.flush().await.ok();
    tokio::io::copy_bidirectional(&mut client, &mut up)
        .await
        .map(|_| ())
        .or_else(ignore_disconnect)
}

/// Inspects one container with a `Connection: close` request, so the whole
/// response arrives before EOF, and returns its decoded body, or `None` when
/// the container does not exist.
async fn inspect(upstream: &std::path::Path, id: &str) -> Result<Option<Value>> {
    let mut up = UnixStream::connect(upstream)
        .await
        .with_context(|| format!("connect upstream {}", upstream.display()))?;
    let request = format!("GET /containers/{id}/json HTTP/1.1\r\nHost: docker\r\nConnection: close\r\n\r\n");
    up.write_all(request.as_bytes()).await.context("write inspect request")?;
    up.flush().await.ok();
    let mut response = Vec::new();
    up.read_to_end(&mut response).await.context("read inspect response")?;
    let (status, body) = split_response(&response)?;
    if status == 404 {
        return Ok(None);
    }
    if status != 200 {
        bail!("inspect {id} returned HTTP {status}");
    }
    let value = serde_json::from_slice(&body).context("decode inspect body")?;
    Ok(Some(value))
}

/// Reads the request head up to and including the blank line. Returns `None` on
/// an immediate clean close.
async fn read_head(client: &mut UnixStream) -> Result<Option<Head>> {
    let mut buffer = Vec::with_capacity(8 << 10);
    let mut chunk = [0u8; 8 << 10];
    loop {
        if let Some(position) = find_subsequence(&buffer, b"\r\n\r\n") {
            let end = position + 4;
            let raw = buffer[..end].to_vec();
            let leftover = buffer[end..].to_vec();
            return parse_head(raw, leftover).map(Some);
        }
        let read = client.read(&mut chunk).await.context("read request head")?;
        if read == 0 {
            return Ok(None);
        }
        buffer.extend_from_slice(&chunk[..read]);
        if buffer.len() > (64 << 10) {
            bail!("request head exceeds 64 KiB");
        }
    }
}

fn parse_head(raw: Vec<u8>, leftover: Vec<u8>) -> Result<Head> {
    let mut headers = [httparse::EMPTY_HEADER; 64];
    let mut request = httparse::Request::new(&mut headers);
    if !request.parse(&raw).context("parse request head")?.is_complete() {
        bail!("incomplete request head");
    }
    let method = request.method.context("request has no method")?.to_string();
    let path = request.path.context("request has no path")?.to_string();
    let mut collected = Vec::new();
    let mut content_length = 0usize;
    for header in request.headers.iter() {
        let value = String::from_utf8_lossy(header.value).to_string();
        if header.name.eq_ignore_ascii_case("content-length") {
            content_length = value.trim().parse().unwrap_or(0);
        }
        collected.push((header.name.to_string(), value));
    }
    Ok(Head { method, path, headers: collected, content_length, raw, leftover })
}

/// Reads the whole request body: the bytes past the head plus however many more
/// Content-Length calls for.
async fn read_body(client: &mut UnixStream, head: &Head) -> Result<Vec<u8>> {
    let mut body = head.leftover.clone();
    while body.len() < head.content_length {
        let mut chunk = [0u8; 8 << 10];
        let read = client.read(&mut chunk).await.context("read request body")?;
        if read == 0 {
            break;
        }
        body.extend_from_slice(&chunk[..read]);
    }
    body.truncate(head.content_length);
    Ok(body)
}

/// Rebuilds a request head for forwarding: the client's headers minus its
/// Connection header, always with `Connection: close`. When the proxy rewrote
/// the body, `override_length` replaces Content-Length and drops any
/// transfer-encoding; otherwise the original length headers are kept.
fn rebuild_request(head: &Head, override_length: Option<usize>) -> Vec<u8> {
    let mut out = format!("{} {} HTTP/1.1\r\n", head.method, head.path);
    for (name, value) in &head.headers {
        let lower = name.to_ascii_lowercase();
        if lower == "connection" {
            continue;
        }
        if override_length.is_some() && (lower == "content-length" || lower == "transfer-encoding") {
            continue;
        }
        out.push_str(&format!("{name}: {value}\r\n"));
    }
    out.push_str("Connection: close\r\n");
    if let Some(length) = override_length {
        out.push_str(&format!("Content-Length: {length}\r\n"));
    }
    out.push_str("\r\n");
    out.into_bytes()
}

/// Splits an HTTP response into its status code and decoded body, de-chunking a
/// chunked transfer encoding.
fn split_response(response: &[u8]) -> Result<(u16, Vec<u8>)> {
    let terminator = find_subsequence(response, b"\r\n\r\n").context("response has no head")?;
    let head = &response[..terminator];
    let body = &response[terminator + 4..];
    let status = parse_status(head)?;
    let chunked = head
        .split(|&b| b == b'\n')
        .any(|line| {
            let line = String::from_utf8_lossy(line);
            let line = line.trim();
            line.to_ascii_lowercase().starts_with("transfer-encoding:") && line.to_ascii_lowercase().contains("chunked")
        });
    let decoded = if chunked { dechunk(body)? } else { body.to_vec() };
    Ok((status, decoded))
}

fn parse_status(head: &[u8]) -> Result<u16> {
    let first = head.split(|&b| b == b'\n').next().context("empty response head")?;
    let line = String::from_utf8_lossy(first);
    let code = line.split_whitespace().nth(1).context("no status code")?;
    code.parse().context("parse status code")
}

/// Decodes an HTTP/1.1 chunked body into its content.
fn dechunk(mut body: &[u8]) -> Result<Vec<u8>> {
    let mut out = Vec::new();
    loop {
        let line_end = find_subsequence(body, b"\r\n").context("chunked: no size line")?;
        let size_line = String::from_utf8_lossy(&body[..line_end]);
        let size = usize::from_str_radix(size_line.trim().split(';').next().unwrap_or("0").trim(), 16)
            .context("chunked: bad size")?;
        body = &body[line_end + 2..];
        if size == 0 {
            break;
        }
        if body.len() < size {
            bail!("chunked: truncated chunk");
        }
        out.extend_from_slice(&body[..size]);
        body = &body[size..];
        if body.starts_with(b"\r\n") {
            body = &body[2..];
        }
    }
    Ok(out)
}

async fn deny(client: &mut UnixStream, reason: &str) -> Result<()> {
    let payload = serde_json::json!({ "message": reason }).to_string();
    let response = format!(
        "HTTP/1.1 403 Forbidden\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{}",
        payload.len(),
        payload
    );
    client.write_all(response.as_bytes()).await.context("write denial")?;
    client.flush().await.ok();
    Ok(())
}

fn ignore_disconnect(error: std::io::Error) -> Result<()> {
    match error.kind() {
        std::io::ErrorKind::BrokenPipe | std::io::ErrorKind::ConnectionReset => Ok(()),
        _ => Err(error).context("splice connection"),
    }
}

fn find_subsequence(haystack: &[u8], needle: &[u8]) -> Option<usize> {
    if needle.is_empty() || haystack.len() < needle.len() {
        return None;
    }
    haystack.windows(needle.len()).position(|window| window == needle)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn finds_the_header_terminator() {
        assert_eq!(find_subsequence(b"GET / HTTP/1.1\r\n\r\nbody", b"\r\n\r\n"), Some(14));
        assert_eq!(find_subsequence(b"no terminator", b"\r\n\r\n"), None);
    }

    #[test]
    fn parses_method_path_and_length() {
        let raw = b"POST /v1.45/containers/create HTTP/1.1\r\nHost: docker\r\nContent-Length: 7\r\n\r\n".to_vec();
        let head = parse_head(raw, b"{\"a\":1}".to_vec()).unwrap();
        assert_eq!(head.method, "POST");
        assert_eq!(head.path, "/v1.45/containers/create");
        assert_eq!(head.content_length, 7);
    }

    #[test]
    fn rebuilds_head_with_new_length_and_without_chunking() {
        let raw = b"POST /containers/create HTTP/1.1\r\nHost: docker\r\nContent-Length: 2\r\nTransfer-Encoding: chunked\r\nConnection: keep-alive\r\n\r\n".to_vec();
        let head = parse_head(raw, Vec::new()).unwrap();
        let rebuilt = String::from_utf8(rebuild_request(&head, Some(42))).unwrap();
        assert!(rebuilt.starts_with("POST /containers/create HTTP/1.1\r\n"));
        assert!(rebuilt.contains("Host: docker\r\n"));
        assert!(rebuilt.contains("Content-Length: 42\r\n"));
        assert!(rebuilt.contains("Connection: close\r\n"));
        assert!(!rebuilt.to_ascii_lowercase().contains("transfer-encoding"));
        assert!(!rebuilt.to_ascii_lowercase().contains("keep-alive"));
    }

    #[test]
    fn rebuilds_head_forcing_close_and_keeping_length() {
        let raw = b"GET /containers/abc/json HTTP/1.1\r\nHost: docker\r\nConnection: keep-alive\r\n\r\n".to_vec();
        let head = parse_head(raw, Vec::new()).unwrap();
        let rebuilt = String::from_utf8(rebuild_request(&head, None)).unwrap();
        assert!(rebuilt.contains("Connection: close\r\n"));
        assert!(!rebuilt.to_ascii_lowercase().contains("keep-alive"));
    }

    #[test]
    fn splits_a_plain_response() {
        let response = b"HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\n{}";
        let (status, body) = split_response(response).unwrap();
        assert_eq!(status, 200);
        assert_eq!(body, b"{}");
    }

    #[test]
    fn splits_a_chunked_response() {
        let response = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\n";
        let (status, body) = split_response(response).unwrap();
        assert_eq!(status, 200);
        assert_eq!(body, b"{}");
    }

    #[test]
    fn reads_a_404_as_absent() {
        let response = b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n";
        let (status, _) = split_response(response).unwrap();
        assert_eq!(status, 404);
    }
}
