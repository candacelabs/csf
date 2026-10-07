// Copyright 2026 Candace Labs

//! The Docker Engine request filter.
//!
//! The proxy forwards a fixed allow-list of Engine API calls and refuses
//! everything else. Classification ([`classify`]) is pure path-and-method
//! matching; the container-create body check ([`check_create`]) is a pure
//! predicate over the decoded JSON. The proxy layers one runtime check on top:
//! an operation naming a container is forwarded only after the proxy confirms
//! that container carries this session's label. `tools/bazel.sh` must work
//! through this filter unchanged, so container create from a pinned image,
//! start, wait, attach, logs, inspect and remove are all allowed; privilege,
//! host namespaces, arbitrary binds and foreign containers are not.

use std::path::PathBuf;

use serde_json::Value;

use crate::landlock_rules::is_within;

/// The label the proxy stamps on every container it creates and requires on
/// every container it is asked to act on.
pub const SESSION_LABEL: &str = "csf.session";

/// A classified Engine request: the shape the proxy acts on.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Request {
    /// `GET /_ping`.
    Ping,
    /// `GET /version`.
    Version,
    /// `GET /images/{ref}/json`.
    ImageInspect,
    /// `POST /containers/create`; the body is checked by [`check_create`].
    ContainerCreate,
    /// An operation on one named container; the proxy verifies its label.
    ContainerOp { id: String, kind: ContainerOp },
}

/// The container-scoped operations the proxy forwards.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ContainerOp {
    Start,
    Wait,
    Attach,
    Inspect,
    Logs,
    Remove,
}

/// Classifies one request, or rejects it with a reason. `path` may carry an
/// Engine version prefix (`/v1.45/...`) and a query string; both are ignored.
pub fn classify(method: &str, path: &str) -> Result<Request, String> {
    let path = path.split('?').next().unwrap_or(path);
    let segments = split(path);
    let segments = strip_version(segments);
    match (method, segments.as_slice()) {
        ("GET", ["_ping"]) => Ok(Request::Ping),
        ("GET", ["version"]) => Ok(Request::Version),
        ("GET", ["images", rest @ ..]) if rest.last() == Some(&"json") && rest.len() >= 2 => {
            Ok(Request::ImageInspect)
        }
        ("POST", ["containers", "create"]) => Ok(Request::ContainerCreate),
        ("POST", ["containers", id, "start"]) => container(id, ContainerOp::Start),
        ("POST", ["containers", id, "wait"]) => container(id, ContainerOp::Wait),
        ("POST", ["containers", id, "attach"]) => container(id, ContainerOp::Attach),
        ("GET", ["containers", id, "json"]) => container(id, ContainerOp::Inspect),
        ("GET", ["containers", id, "logs"]) => container(id, ContainerOp::Logs),
        ("DELETE", ["containers", id]) => container(id, ContainerOp::Remove),
        _ => Err(format!("refused: {method} {path} is not on the allow-list")),
    }
}

fn container(id: &str, kind: ContainerOp) -> Result<Request, String> {
    if id.is_empty() || !id.bytes().all(|b| b.is_ascii_alphanumeric() || b"_.-".contains(&b)) {
        return Err(format!("refused: {id:?} is not a valid container id"));
    }
    Ok(Request::ContainerOp { id: id.to_string(), kind })
}

fn split(path: &str) -> Vec<&str> {
    path.split('/').filter(|segment| !segment.is_empty()).collect()
}

fn strip_version(segments: Vec<&str>) -> Vec<&str> {
    if let Some(first) = segments.first() {
        if is_version(first) {
            return segments[1..].to_vec();
        }
    }
    segments
}

fn is_version(segment: &str) -> bool {
    let Some(rest) = segment.strip_prefix('v') else {
        return false;
    };
    let mut parts = rest.split('.');
    let (Some(major), Some(minor), None) = (parts.next(), parts.next(), parts.next()) else {
        return false;
    };
    !major.is_empty()
        && !minor.is_empty()
        && major.bytes().all(|b| b.is_ascii_digit())
        && minor.bytes().all(|b| b.is_ascii_digit())
}

/// What a container-create body is checked against.
pub struct CreateLimits<'a> {
    /// The image references the session may run: the digests the repository
    /// pins, read from `bazel/execution_image.txt` and any other pinned image.
    pub allowed_images: &'a [String],
    /// The paths a bind mount's source must lie within: the session's worktree,
    /// run directory and caches.
    pub session_paths: &'a [PathBuf],
}

/// Accepts a create body only when it runs a pinned image with no privilege, no
/// host namespace, and no mount outside the session's own paths. Returns the
/// reason on the first violation.
pub fn check_create(body: &Value, limits: &CreateLimits) -> Result<(), String> {
    let image = body.get("Image").and_then(Value::as_str).unwrap_or_default();
    if !image_allowed(image, limits.allowed_images) {
        return Err(format!("refused: image {image:?} is not a pinned image"));
    }
    if is_nonempty_object(body.get("Volumes")) {
        return Err("refused: anonymous Volumes are not allowed".to_string());
    }
    let host = match body.get("HostConfig") {
        None | Some(Value::Null) => return Ok(()),
        Some(host) => host,
    };
    check_host_config(host, limits)
}

fn check_host_config(host: &Value, limits: &CreateLimits) -> Result<(), String> {
    if host.get("Privileged").and_then(Value::as_bool) == Some(true) {
        return Err("refused: Privileged is not allowed".to_string());
    }
    for field in ["CapAdd", "Devices", "SecurityOpt", "VolumesFrom"] {
        if is_nonempty_array(host.get(field)) {
            return Err(format!("refused: {field} is not allowed"));
        }
    }
    for (field, forbidden) in [("NetworkMode", "host"), ("PidMode", "host"), ("IpcMode", "host")] {
        if host.get(field).and_then(Value::as_str) == Some(forbidden) {
            return Err(format!("refused: {field} {forbidden} is not allowed"));
        }
    }
    check_binds(host.get("Binds"), limits)?;
    check_mounts(host.get("Mounts"), limits)?;
    Ok(())
}

fn check_binds(binds: Option<&Value>, limits: &CreateLimits) -> Result<(), String> {
    let Some(binds) = binds.and_then(Value::as_array) else {
        return Ok(());
    };
    for bind in binds {
        let spec = bind.as_str().unwrap_or_default();
        let source = spec.split(':').next().unwrap_or_default();
        if !source.starts_with('/') {
            return Err(format!("refused: bind {spec:?} has no absolute source"));
        }
        if !is_within(std::path::Path::new(source), limits.session_paths) {
            return Err(format!("refused: bind source {source:?} is outside the session's paths"));
        }
    }
    Ok(())
}

fn check_mounts(mounts: Option<&Value>, limits: &CreateLimits) -> Result<(), String> {
    let Some(mounts) = mounts.and_then(Value::as_array) else {
        return Ok(());
    };
    for mount in mounts {
        let kind = mount.get("Type").and_then(Value::as_str).unwrap_or("bind");
        if kind != "bind" {
            return Err(format!("refused: mount type {kind:?} is not allowed; only bind"));
        }
        let source = mount.get("Source").and_then(Value::as_str).unwrap_or_default();
        if !is_within(std::path::Path::new(source), limits.session_paths) {
            return Err(format!("refused: mount source {source:?} is outside the session's paths"));
        }
    }
    Ok(())
}

/// Returns the body with the session label added, so every container the
/// session creates is attributable and the proxy can verify it later.
pub fn with_session_label(mut body: Value, session_id: &str) -> Value {
    let labels = body
        .as_object_mut()
        .map(|object| object.entry("Labels").or_insert_with(|| Value::Object(Default::default())));
    if let Some(Value::Object(labels)) = labels {
        labels.insert(SESSION_LABEL.to_string(), Value::String(session_id.to_string()));
    }
    body
}

/// Reports whether a container's inspect output carries this session's label.
pub fn container_belongs(inspect: &Value, session_id: &str) -> bool {
    inspect
        .get("Config")
        .and_then(|config| config.get("Labels"))
        .and_then(Value::as_object)
        .and_then(|labels| labels.get(SESSION_LABEL))
        .and_then(Value::as_str)
        == Some(session_id)
}

fn image_allowed(image: &str, allowed: &[String]) -> bool {
    allowed.iter().any(|candidate| candidate == image || same_digest(candidate, image))
}

fn same_digest(a: &str, b: &str) -> bool {
    match (a.split_once("@sha256:"), b.split_once("@sha256:")) {
        (Some((_, left)), Some((_, right))) => left == right,
        _ => false,
    }
}

fn is_nonempty_array(value: Option<&Value>) -> bool {
    value.and_then(Value::as_array).is_some_and(|array| !array.is_empty())
}

fn is_nonempty_object(value: Option<&Value>) -> bool {
    value.and_then(Value::as_object).is_some_and(|object| !object.is_empty())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const IMAGE: &str = "gcr.io/bazel-public/bazel:9.2.0@sha256:deadbeef";

    fn allowed() -> Vec<String> {
        vec![IMAGE.to_string()]
    }
    fn paths() -> Vec<PathBuf> {
        vec![PathBuf::from("/run/s1")]
    }

    #[test]
    fn allows_ping_version_and_image_inspect() {
        assert_eq!(classify("GET", "/_ping"), Ok(Request::Ping));
        assert_eq!(classify("GET", "/v1.45/version"), Ok(Request::Version));
        assert_eq!(
            classify("GET", "/images/alpine%3Alatest/json"),
            Ok(Request::ImageInspect)
        );
        assert_eq!(classify("GET", "/v1.45/images/repo/name/json"), Ok(Request::ImageInspect));
    }

    #[test]
    fn allows_the_container_lifecycle() {
        assert_eq!(classify("POST", "/containers/create"), Ok(Request::ContainerCreate));
        assert_eq!(
            classify("POST", "/v1.45/containers/abc123/start"),
            Ok(Request::ContainerOp { id: "abc123".into(), kind: ContainerOp::Start })
        );
        assert_eq!(
            classify("GET", "/containers/abc123/json?size=false"),
            Ok(Request::ContainerOp { id: "abc123".into(), kind: ContainerOp::Inspect })
        );
        assert_eq!(
            classify("DELETE", "/containers/abc123"),
            Ok(Request::ContainerOp { id: "abc123".into(), kind: ContainerOp::Remove })
        );
    }

    #[test]
    fn refuses_everything_off_the_allow_list() {
        assert!(classify("GET", "/containers/json").is_err()); // list
        assert!(classify("POST", "/images/create").is_err()); // pull
        assert!(classify("GET", "/networks").is_err());
        assert!(classify("POST", "/volumes/create").is_err());
        assert!(classify("GET", "/exec/abc/json").is_err());
        assert!(classify("POST", "/containers/abc/exec").is_err());
        assert!(classify("POST", "/containers/abc/kill").is_err());
    }

    #[test]
    fn accepts_a_pinned_image_with_a_bind_inside_the_session() {
        let body = json!({
            "Image": IMAGE,
            "HostConfig": {"Binds": ["/run/s1/worktree:/w"]}
        });
        check_create(&body, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).unwrap();
    }

    #[test]
    fn rejects_an_unpinned_image() {
        let body = json!({"Image": "alpine:latest"});
        let error = check_create(&body, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).unwrap_err();
        assert!(error.contains("pinned"), "{error}");
    }

    #[test]
    fn accepts_image_by_digest_alone() {
        let body = json!({"Image": "bazel@sha256:deadbeef"});
        check_create(&body, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).unwrap();
    }

    #[test]
    fn rejects_privilege_and_host_namespaces() {
        for host in [
            json!({"Privileged": true}),
            json!({"CapAdd": ["SYS_ADMIN"]}),
            json!({"Devices": [{"PathOnHost": "/dev/kmsg"}]}),
            json!({"SecurityOpt": ["seccomp=unconfined"]}),
            json!({"VolumesFrom": ["other"]}),
            json!({"NetworkMode": "host"}),
            json!({"PidMode": "host"}),
            json!({"IpcMode": "host"}),
        ] {
            let body = json!({"Image": IMAGE, "HostConfig": host});
            assert!(
                check_create(&body, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).is_err(),
                "should reject {host}"
            );
        }
    }

    #[test]
    fn rejects_a_bind_outside_the_session() {
        let body = json!({"Image": IMAGE, "HostConfig": {"Binds": ["/etc:/etc"]}});
        let error = check_create(&body, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).unwrap_err();
        assert!(error.contains("outside"), "{error}");
    }

    #[test]
    fn rejects_a_volume_mount_and_anonymous_volumes() {
        let volume = json!({"Image": IMAGE, "HostConfig": {"Mounts": [{"Type": "volume", "Source": "v"}]}});
        assert!(check_create(&volume, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).is_err());
        let anon = json!({"Image": IMAGE, "Volumes": {"/data": {}}});
        assert!(check_create(&anon, &CreateLimits { allowed_images: &allowed(), session_paths: &paths() }).is_err());
    }

    #[test]
    fn labels_the_create_body() {
        let labelled = with_session_label(json!({"Image": IMAGE}), "s1");
        assert_eq!(labelled["Labels"][SESSION_LABEL], json!("s1"));
    }

    #[test]
    fn container_belongs_reads_the_label() {
        let inspect = json!({"Config": {"Labels": {SESSION_LABEL: "s1"}}});
        assert!(container_belongs(&inspect, "s1"));
        assert!(!container_belongs(&inspect, "s2"));
        assert!(!container_belongs(&json!({"Config": {"Labels": {}}}), "s1"));
    }
}
