//! Phase 49: blob-adapter — Apache OpenDAL as the platform's unified object
//! storage engine.
//!
//! OpenDAL is Rust-native; the Go core-api reaches it through this sidecar
//! over HTTP, exactly like the other adapters (mojaloop, whatsapp, email,
//! voice). All binary payloads (tenant media today) live in whatever
//! OpenDAL service is configured — fs for dev, s3/gcs/azblob in
//! production — selected entirely by environment:
//!
//!   CE_BLOB_SCHEME   fs | memory | s3 | gcs | azblob | ... (default: fs)
//!   OPENDAL_*        service config, passed through verbatim lowercased
//!                    (OPENDAL_ROOT, OPENDAL_BUCKET, OPENDAL_ENDPOINT, ...)
//!   CE_BLOB_ROOT     convenience alias for OPENDAL_ROOT (fs default
//!                    /var/lib/ce-blobs)
//!   CE_BLOB_LISTEN   bind address (default 0.0.0.0:8230)
//!
//! Keys are tenant-scoped: callers pass X-Tenant-ID and a logical key; the
//! object path is always `{tenant}/{key}`. Keys are validated (no `..`, no
//! empty segments, strict charset) so a tenant can never escape its prefix.

use std::collections::HashMap;

use axum::body::Bytes;
use axum::extract::{DefaultBodyLimit, Path, State};
use axum::http::{header, HeaderMap, HeaderValue, StatusCode};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, put};
use axum::{Json, Router};
use opendal::{ErrorKind, Operator};
use serde::Serialize;

/// AppState is the OpenDAL operator plus the scheme name (for provenance
/// reporting — the Go core labels every stored row with the engine).
#[derive(Clone)]
pub struct AppState {
    op: Operator,
    scheme: String,
}

/// build_operator constructs the OpenDAL operator from a scheme and a
/// config map. Kept separate from env parsing so tests build operators
/// directly (memory/fs) without touching process env.
pub fn build_operator(scheme: &str, map: HashMap<String, String>) -> Result<Operator, String> {
    // via_iter takes the scheme by name ("fs", "memory", "s3", ...) and
    // resolves it through the service registry — unknown schemes and bad
    // config both surface here as startup errors, never at first request.
    Operator::via_iter(scheme, map).map_err(|e| format!("operator init ({scheme}): {e}"))
}

/// app builds the HTTP surface over a ready operator.
pub fn app(op: Operator, scheme: &str) -> Router {
    let state = AppState {
        op,
        scheme: scheme.to_string(),
    };
    Router::new()
        .route(
            "/v1/blobs/*key",
            put(put_blob)
                .get(get_blob)
                .head(head_blob)
                .delete(delete_blob),
        )
        .route("/v1/engine", get(engine))
        .route("/healthz", get(healthz))
        // Media uploads cap at 8 MB decoded; 16 MB leaves headroom for
        // future blob kinds without opening an unbounded body.
        .layer(DefaultBodyLimit::max(16 << 20))
        .with_state(state)
}

#[derive(Serialize)]
struct EngineInfo {
    engine: &'static str,
    scheme: String,
}

async fn healthz(State(st): State<AppState>) -> impl IntoResponse {
    Json(serde_json::json!({ "status": "ok", "scheme": st.scheme }))
}

async fn engine(State(st): State<AppState>) -> impl IntoResponse {
    Json(EngineInfo {
        engine: "apache-opendal",
        scheme: st.scheme,
    })
}

/// valid_key enforces the key grammar: `/`-separated segments of
/// [A-Za-z0-9._-], no empty segments, no `.`/`..` segments, 512 chars max.
/// This is what makes the tenant prefix a real boundary — nothing in the
/// key can traverse out of `{tenant}/`.
fn valid_key(key: &str) -> bool {
    if key.is_empty() || key.len() > 512 {
        return false;
    }
    key.split('/').all(|seg| {
        !seg.is_empty()
            && seg != "."
            && seg != ".."
            && seg
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '_' | '-'))
    })
}

struct ReqCtx {
    tenant: String,
    path: String,
}

/// req_ctx extracts + validates the tenant header and the wildcard key,
/// returning the fully-qualified object path `{tenant}/{key}`.
fn req_ctx(headers: &HeaderMap, key: &str) -> Result<ReqCtx, (StatusCode, String)> {
    let tenant = headers
        .get("x-tenant-id")
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .trim()
        .to_string();
    if tenant.is_empty() {
        return Err((
            StatusCode::BAD_REQUEST,
            "missing X-Tenant-ID header".to_string(),
        ));
    }
    if !valid_key(key) {
        return Err((StatusCode::BAD_REQUEST, format!("invalid blob key: {key}")));
    }
    Ok(ReqCtx {
        path: format!("{tenant}/{key}"),
        tenant,
    })
}

fn internal_err(context: &str, e: opendal::Error) -> Response {
    tracing::error!(%context, error = %e, "blob-adapter opendal error");
    (StatusCode::INTERNAL_SERVER_ERROR, format!("{context}: {e}")).into_response()
}

async fn put_blob(
    State(st): State<AppState>,
    headers: HeaderMap,
    Path(key): Path<String>,
    body: Bytes,
) -> Response {
    let cx = match req_ctx(&headers, &key) {
        Ok(cx) => cx,
        Err((code, msg)) => return (code, msg).into_response(),
    };
    if body.is_empty() {
        return (StatusCode::BAD_REQUEST, "empty blob body".to_string()).into_response();
    }
    // Persist the caller's content type with the object so GET/HEAD can
    // serve it back faithfully (OpenDAL stores it as object metadata).
    let content_type = headers
        .get(header::CONTENT_TYPE)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("application/octet-stream")
        .to_string();
    let size = body.len();
    if let Err(e) = st
        .op
        .write_with(&cx.path, body)
        .content_type(&content_type)
        .await
    {
        return internal_err("write", e);
    }
    (
        StatusCode::CREATED,
        Json(serde_json::json!({
            "tenant_id": cx.tenant,
            "key": key,
            "size_bytes": size,
        })),
    )
        .into_response()
}

async fn get_blob(
    State(st): State<AppState>,
    headers: HeaderMap,
    Path(key): Path<String>,
) -> Response {
    let cx = match req_ctx(&headers, &key) {
        Ok(cx) => cx,
        Err((code, msg)) => return (code, msg).into_response(),
    };
    // Stat first: the content type lives in object metadata and we want a
    // clean 404 without buffering a read.
    let meta = match st.op.stat(&cx.path).await {
        Ok(m) => m,
        Err(e) if e.kind() == ErrorKind::NotFound => {
            return (StatusCode::NOT_FOUND, "blob not found".to_string()).into_response()
        }
        Err(e) => return internal_err("stat", e),
    };
    let buf = match st.op.read(&cx.path).await {
        Ok(b) => b,
        Err(e) if e.kind() == ErrorKind::NotFound => {
            return (StatusCode::NOT_FOUND, "blob not found".to_string()).into_response()
        }
        Err(e) => return internal_err("read", e),
    };
    let ct = meta
        .content_type()
        .unwrap_or("application/octet-stream")
        .to_string();
    let mut res = buf.to_vec().into_response();
    res.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_str(&ct).unwrap_or(HeaderValue::from_static("application/octet-stream")),
    );
    res
}

async fn head_blob(
    State(st): State<AppState>,
    headers: HeaderMap,
    Path(key): Path<String>,
) -> Response {
    let cx = match req_ctx(&headers, &key) {
        Ok(cx) => cx,
        Err((code, msg)) => return (code, msg).into_response(),
    };
    match st.op.stat(&cx.path).await {
        Ok(meta) => {
            let mut res = StatusCode::OK.into_response();
            let h = res.headers_mut();
            h.insert(
                header::CONTENT_LENGTH,
                HeaderValue::from_str(&meta.content_length().to_string())
                    .unwrap_or(HeaderValue::from_static("0")),
            );
            if let Some(ct) = meta.content_type() {
                if let Ok(v) = HeaderValue::from_str(ct) {
                    h.insert(header::CONTENT_TYPE, v);
                }
            }
            res
        }
        Err(e) if e.kind() == ErrorKind::NotFound => {
            (StatusCode::NOT_FOUND, "blob not found".to_string()).into_response()
        }
        Err(e) => internal_err("stat", e),
    }
}

async fn delete_blob(
    State(st): State<AppState>,
    headers: HeaderMap,
    Path(key): Path<String>,
) -> Response {
    let cx = match req_ctx(&headers, &key) {
        Ok(cx) => cx,
        Err((code, msg)) => return (code, msg).into_response(),
    };
    // OpenDAL delete is idempotent — deleting a missing key is not an
    // error, which is exactly the semantics the core's delete path wants.
    match st.op.delete(&cx.path).await {
        Ok(()) => StatusCode::NO_CONTENT.into_response(),
        Err(e) => internal_err("delete", e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::body::Body;
    use axum::http::{Request, StatusCode};
    use http_body_util::BodyExt;
    use tower::ServiceExt;

    fn mem_app() -> Router {
        let op = build_operator("memory", HashMap::new()).expect("memory operator");
        app(op, "memory")
    }

    fn req(
        method: &str,
        uri: &str,
        tenant: &str,
        body: Vec<u8>,
        ct: Option<&str>,
    ) -> Request<Body> {
        let mut b = Request::builder()
            .method(method)
            .uri(uri)
            .header("x-tenant-id", tenant);
        if let Some(ct) = ct {
            b = b.header("content-type", ct);
        }
        b.body(Body::from(body)).unwrap()
    }

    async fn body_bytes(res: Response) -> Vec<u8> {
        res.into_body().collect().await.unwrap().to_bytes().to_vec()
    }

    #[tokio::test]
    async fn memory_roundtrip() {
        let app = mem_app();
        let payload = b"\x89PNG-fake-but-bytes".to_vec();

        let res = app
            .clone()
            .oneshot(req(
                "PUT",
                "/v1/blobs/media/m-1",
                "t1",
                payload.clone(),
                Some("image/png"),
            ))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::CREATED);

        let res = app
            .clone()
            .oneshot(req("GET", "/v1/blobs/media/m-1", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::OK);
        assert_eq!(
            res.headers().get("content-type").unwrap().to_str().unwrap(),
            "image/png"
        );
        assert_eq!(body_bytes(res).await, payload);

        let res = app
            .clone()
            .oneshot(req("HEAD", "/v1/blobs/media/m-1", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::OK);
        assert_eq!(
            res.headers()
                .get("content-length")
                .unwrap()
                .to_str()
                .unwrap(),
            payload.len().to_string()
        );

        let res = app
            .clone()
            .oneshot(req("DELETE", "/v1/blobs/media/m-1", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::NO_CONTENT);

        let res = app
            .oneshot(req("GET", "/v1/blobs/media/m-1", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::NOT_FOUND);
    }

    #[tokio::test]
    async fn tenant_isolation() {
        let app = mem_app();
        let res = app
            .clone()
            .oneshot(req(
                "PUT",
                "/v1/blobs/media/m-1",
                "t1",
                b"secret".to_vec(),
                None,
            ))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::CREATED);
        // Same key, other tenant: 404 — the tenant prefix is the boundary.
        let res = app
            .clone()
            .oneshot(req("GET", "/v1/blobs/media/m-1", "t2", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::NOT_FOUND);
        // Delete is tenant-scoped too.
        let res = app
            .oneshot(req("DELETE", "/v1/blobs/media/m-1", "t2", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::NO_CONTENT);
    }

    #[tokio::test]
    async fn rejects_bad_keys_and_missing_tenant() {
        let app = mem_app();
        for bad in [
            "../etc/passwd",
            "a//b",
            "a/./b",
            "a%20b/c",
            "a//",
            "%2e%2e/x",
        ] {
            let uri = format!("/v1/blobs/{bad}");
            let res = app
                .clone()
                .oneshot(req("PUT", &uri, "t1", b"x".to_vec(), None))
                .await
                .unwrap();
            assert!(
                res.status() == StatusCode::BAD_REQUEST || res.status() == StatusCode::NOT_FOUND,
                "key {bad} must be rejected, got {}",
                res.status()
            );
        }
        // Missing tenant header → 400.
        let r = Request::builder()
            .method("PUT")
            .uri("/v1/blobs/media/m-9")
            .body(Body::from("x"))
            .unwrap();
        let res = app.clone().oneshot(r).await.unwrap();
        assert_eq!(res.status(), StatusCode::BAD_REQUEST);
        // Empty body → 400.
        let res = app
            .oneshot(req("PUT", "/v1/blobs/media/m-9", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::BAD_REQUEST);
    }

    #[tokio::test]
    async fn engine_endpoint_reports_opendal() {
        let app = mem_app();
        let res = app
            .oneshot(req("GET", "/v1/engine", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::OK);
        let body = body_bytes(res).await;
        let v: serde_json::Value = serde_json::from_slice(&body).unwrap();
        assert_eq!(v["engine"], "apache-opendal");
        assert_eq!(v["scheme"], "memory");
    }

    #[tokio::test]
    async fn fs_backend_writes_real_files() {
        // fs scheme: prove the OpenDAL service layer lands bytes on disk at
        // {root}/{tenant}/{key}.
        let root = std::env::temp_dir().join(format!(
            "ce-blob-test-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let mut map = HashMap::new();
        map.insert("root".to_string(), root.to_string_lossy().to_string());
        let op = build_operator("fs", map).expect("fs operator");
        let app = app(op, "fs");

        let res = app
            .clone()
            .oneshot(req(
                "PUT",
                "/v1/blobs/media/m-2",
                "t1",
                b"on-disk".to_vec(),
                Some("image/jpeg"),
            ))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::CREATED);
        let on_disk = root.join("t1").join("media").join("m-2");
        assert_eq!(std::fs::read(&on_disk).unwrap(), b"on-disk");

        let res = app
            .oneshot(req("GET", "/v1/blobs/media/m-2", "t1", vec![], None))
            .await
            .unwrap();
        assert_eq!(res.status(), StatusCode::OK);
        assert_eq!(body_bytes(res).await, b"on-disk");

        let _ = std::fs::remove_dir_all(&root);
    }

    #[test]
    fn key_grammar() {
        assert!(valid_key("media/abc-123.png"));
        assert!(valid_key("a"));
        assert!(!valid_key(""));
        assert!(!valid_key(".."));
        assert!(!valid_key("a/../b"));
        assert!(!valid_key("a//b"));
        assert!(!valid_key("/a"));
        assert!(!valid_key("a/"));
        assert!(!valid_key("a b"));
        assert!(!valid_key(&"x".repeat(513)));
    }
}
