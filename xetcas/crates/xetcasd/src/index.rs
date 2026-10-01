//! The SQLite metadata index.
//!
//! Access pattern: a small free-list of connections, each checked out inside a
//! `spawn_blocking` call. rusqlite is synchronous and `Connection` is not
//! `Sync`, so some form of hand-off is mandatory; a free-list beats a single
//! shared writer because reconstruction and LFS download are read-heavy and
//! WAL mode lets those readers proceed concurrently with the one writer.
//! Write serialization is left to SQLite itself, with `busy_timeout` absorbing
//! the brief contention window rather than surfacing SQLITE_BUSY to a client.

use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};

use prost::Message;
use rusqlite::{Connection, OptionalExtension};
use xetcas_contracts::v1::{FileRecord, XorbRecord};

use crate::error::{AppError, AppResult};

/// Current on-disk schema version.
const SCHEMA_VERSION: i64 = 1;

/// Versions this binary can safely read and write. A version is a compatibility
/// contract, not an advisory marker: opening an unknown version must not run
/// schema DDL, which could otherwise mutate a database this binary cannot
/// interpret correctly.
const SUPPORTED_SCHEMA_VERSIONS: &[i64] = &[SCHEMA_VERSION];

/// Handle to the metadata index. Cheap to clone.
#[derive(Clone)]
pub struct Index {
    inner: Arc<Inner>,
}

struct Inner {
    path: PathBuf,
    idle: Mutex<Vec<Connection>>,
}

impl Inner {
    fn open_connection(&self) -> rusqlite::Result<Connection> {
        let conn = Connection::open(&self.path)?;
        conn.busy_timeout(std::time::Duration::from_secs(10))?;
        conn.pragma_update(None, "journal_mode", "WAL")?;
        // FULL, not NORMAL: an acknowledged xorb/shard upload must survive power
        // loss, or the client's weeks-long shard cache will later reference a
        // xorb the server lost and every push 400s with a non-retried error.
        conn.pragma_update(None, "synchronous", "FULL")?;
        Ok(conn)
    }

    fn checkout(&self) -> rusqlite::Result<Connection> {
        if let Some(conn) = self.idle.lock().expect("index pool poisoned").pop() {
            return Ok(conn);
        }
        self.open_connection()
    }

    fn checkin(&self, conn: Connection) {
        let mut idle = self.idle.lock().expect("index pool poisoned");
        // Cap the free list; bursty concurrency should not pin file handles.
        if idle.len() < 16 {
            idle.push(conn);
        }
    }
}

impl Index {
    /// Open (creating if needed) the index at `path`.
    ///
    /// Existing databases are inspected before a pooled connection enables WAL
    /// or executes any DDL. Only an empty database is initialized here; a
    /// database without version metadata and a database at an unsupported
    /// version both require an explicit migration from a compatible binary.
    pub async fn open(path: &Path) -> AppResult<Self> {
        let inner = Arc::new(Inner {
            path: path.to_path_buf(),
            idle: Mutex::new(Vec::new()),
        });
        let this = Self { inner };
        if this.needs_initial_schema().await? {
            this.with(|conn| {
                let tx = conn.transaction()?;
                tx.execute_batch(INITIAL_SCHEMA)?;
                tx.execute(
                    "INSERT INTO schema_meta (version) VALUES (?1)",
                    [SCHEMA_VERSION],
                )?;
                tx.commit()
            })
            .await?;
        }
        Ok(this)
    }

    /// Returns true only for an empty SQLite database. This deliberately uses
    /// a bare connection: `open_connection` configures WAL, which is a write
    /// and therefore cannot precede the compatibility check.
    async fn needs_initial_schema(&self) -> AppResult<bool> {
        let path = self.inner.path.clone();
        tokio::task::spawn_blocking(move || inspect_schema(&path))
            .await
            .map_err(|e| AppError::internal(format!("index schema task: {e}")))?
    }

    async fn with<T, F>(&self, f: F) -> AppResult<T>
    where
        F: FnOnce(&mut Connection) -> rusqlite::Result<T> + Send + 'static,
        T: Send + 'static,
    {
        let inner = self.inner.clone();
        tokio::task::spawn_blocking(move || {
            let mut conn = inner.checkout()?;
            let out = f(&mut conn);
            // Return the connection to the pool whether or not the closure
            // succeeded; a failed statement does not poison the handle.
            inner.checkin(conn);
            out
        })
        .await
        .map_err(|e| AppError::internal(format!("index task: {e}")))?
        .map_err(AppError::from)
    }
}

/// The complete v1 schema. Future versions must add an explicit migration and
/// extend `SUPPORTED_SCHEMA_VERSIONS`; this initializer is only for new files.
const INITIAL_SCHEMA: &str = "
CREATE TABLE IF NOT EXISTS schema_meta (version INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS xorbs (
    hash   TEXT PRIMARY KEY,
    record BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS files (
    file_hash TEXT PRIMARY KEY,
    sha256    TEXT,
    record    BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS files_sha256 ON files (sha256);
CREATE TABLE IF NOT EXISTS chunks (
    chunk_hash  TEXT PRIMARY KEY,
    xorb_hash   TEXT NOT NULL,
    chunk_index INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS store_stats (
    id         INTEGER PRIMARY KEY CHECK (id = 0),
    disk_bytes INTEGER NOT NULL
);
INSERT OR IGNORE INTO store_stats (id, disk_bytes) VALUES (0, 0);
";

/// Inspect schema metadata without applying pragmas or DDL.
fn inspect_schema(path: &Path) -> AppResult<bool> {
    let conn = Connection::open(path)?;
    let schema_meta_exists: bool = conn.query_row(
        "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'schema_meta')",
        [],
        |row| row.get(0),
    )?;
    if !schema_meta_exists {
        let user_object_count: i64 = conn.query_row(
            "SELECT COUNT(*) FROM sqlite_master WHERE name NOT GLOB 'sqlite_*'",
            [],
            |row| row.get(0),
        )?;
        if user_object_count == 0 {
            return Ok(true);
        }
        return Err(AppError::internal(
            "index has schema objects but no schema version metadata; explicit migration required",
        ));
    }

    let versions = conn
        .prepare("SELECT version FROM schema_meta")?
        .query_map([], |row| row.get::<_, i64>(0))?
        .collect::<rusqlite::Result<Vec<_>>>()?;
    if versions.len() != 1 {
        return Err(AppError::internal(format!(
            "index schema metadata must contain exactly one version, found {}",
            versions.len()
        )));
    }
    let version = versions[0];
    if !SUPPORTED_SCHEMA_VERSIONS.contains(&version) {
        return Err(AppError::internal(format!(
            "unsupported index schema version {version}; supported versions: {:?}",
            SUPPORTED_SCHEMA_VERSIONS
        )));
    }
    Ok(false)
}

fn decode_xorb(blob: Vec<u8>) -> rusqlite::Result<XorbRecord> {
    XorbRecord::decode(blob.as_slice()).map_err(|e| {
        rusqlite::Error::FromSqlConversionFailure(0, rusqlite::types::Type::Blob, Box::new(e))
    })
}

fn decode_file(blob: Vec<u8>) -> rusqlite::Result<FileRecord> {
    FileRecord::decode(blob.as_slice()).map_err(|e| {
        rusqlite::Error::FromSqlConversionFailure(0, rusqlite::types::Type::Blob, Box::new(e))
    })
}

impl Index {
    /// Register a verified xorb plus the chunks of it that are eligible to
    /// answer a global-dedup probe. Returns false when the xorb already
    /// existed: xorb upload is idempotent (docs/research/dataplane.md 8.1).
    ///
    /// `disk_bytes` is the size of the object as written to the store. It is
    /// accumulated in the same transaction as the record, so `/health` can
    /// report stored bytes without walking the object tree.
    pub async fn put_xorb(
        &self,
        record: XorbRecord,
        dedup_chunks: Vec<(String, u32)>,
        disk_bytes: u64,
    ) -> AppResult<bool> {
        let hash = record.xorb_hash.clone();
        let blob = record.encode_to_vec();
        self.with(move |conn| {
            let tx = conn.transaction()?;
            let inserted = tx.execute(
                "INSERT OR IGNORE INTO xorbs (hash, record) VALUES (?1, ?2)",
                rusqlite::params![&hash, &blob],
            )? > 0;
            if inserted {
                let mut stmt = tx.prepare(
                    "INSERT OR IGNORE INTO chunks (chunk_hash, xorb_hash, chunk_index) VALUES (?1, ?2, ?3)",
                )?;
                for (chunk_hash, index) in &dedup_chunks {
                    stmt.execute(rusqlite::params![chunk_hash, &hash, index])?;
                }
                drop(stmt);
                tx.execute(
                    "UPDATE store_stats SET disk_bytes = disk_bytes + ?1 WHERE id = 0",
                    [disk_bytes as i64],
                )?;
            }
            tx.commit()?;
            Ok(inserted)
        })
        .await
    }

    /// Fetch one xorb record.
    pub async fn get_xorb(&self, hash: &str) -> AppResult<Option<XorbRecord>> {
        let hash = hash.to_string();
        self.with(move |conn| {
            let blob: Option<Vec<u8>> = conn
                .query_row("SELECT record FROM xorbs WHERE hash = ?1", [&hash], |r| {
                    r.get(0)
                })
                .optional()?;
            blob.map(decode_xorb).transpose()
        })
        .await
    }

    /// Fetch several xorb records at once, skipping any that are missing.
    pub async fn get_xorbs(&self, hashes: Vec<String>) -> AppResult<HashMap<String, XorbRecord>> {
        self.with(move |conn| {
            let mut stmt = conn.prepare("SELECT record FROM xorbs WHERE hash = ?1")?;
            let mut out = HashMap::with_capacity(hashes.len());
            for hash in hashes {
                let blob: Option<Vec<u8>> = stmt.query_row([&hash], |r| r.get(0)).optional()?;
                if let Some(blob) = blob {
                    out.insert(hash, decode_xorb(blob)?);
                }
            }
            Ok(out)
        })
        .await
    }
}

impl Index {
    /// Look up a file by its xet file hash.
    pub async fn get_file(&self, file_hash: &str) -> AppResult<Option<FileRecord>> {
        let file_hash = file_hash.to_string();
        self.with(move |conn| {
            let blob: Option<Vec<u8>> = conn
                .query_row(
                    "SELECT record FROM files WHERE file_hash = ?1",
                    [&file_hash],
                    |r| r.get(0),
                )
                .optional()?;
            blob.map(decode_file).transpose()
        })
        .await
    }

    /// Look up a file by the SHA-256 recorded in its shard metadata. This is
    /// the linkage that lets the LFS bridge serve a download by git-lfs oid
    /// (docs/research/git-xet.md section 6.4).
    pub async fn file_by_sha256(&self, sha256: &str) -> AppResult<Option<FileRecord>> {
        let sha256 = sha256.to_string();
        self.with(move |conn| {
            let blob: Option<Vec<u8>> = conn
                .query_row(
                    "SELECT record FROM files WHERE sha256 = ?1 ORDER BY file_hash LIMIT 1",
                    [&sha256],
                    |r| r.get(0),
                )
                .optional()?;
            blob.map(decode_file).transpose()
        })
        .await
    }

    /// Register file records and index any additional dedup-eligible chunks
    /// the shard told us about. Returns the number of files that were new.
    ///
    /// A verified SHA-256 arriving after an earlier no-SHA record upgrades the
    /// record atomically. Once present, a SHA-256 is trusted and immutable:
    /// even a conflicting later record cannot overwrite it.
    pub async fn put_files(
        &self,
        files: Vec<FileRecord>,
        chunks: Vec<(String, String, u32)>,
    ) -> AppResult<usize> {
        self.with(move |conn| {
            let tx = conn.transaction()?;
            let mut new_files = 0usize;
            {
                let mut stmt = tx.prepare(
                    "INSERT OR IGNORE INTO files (file_hash, sha256, record) VALUES (?1, ?2, ?3)",
                )?;
                let mut upgrade_stmt = tx.prepare(
                    "UPDATE files SET sha256 = ?2, record = ?3 WHERE file_hash = ?1 AND sha256 IS NULL",
                )?;
                for file in &files {
                    let sha = if file.sha256.is_empty() { None } else { Some(&file.sha256) };
                    let blob = file.encode_to_vec();
                    let inserted = stmt.execute(rusqlite::params![&file.file_hash, sha, &blob])?;
                    new_files += inserted;
                    if inserted == 0 && sha.is_some() {
                        upgrade_stmt.execute(rusqlite::params![&file.file_hash, sha, &blob])?;
                    }
                }
                let mut chunk_stmt = tx.prepare(
                    "INSERT OR IGNORE INTO chunks (chunk_hash, xorb_hash, chunk_index) VALUES (?1, ?2, ?3)",
                )?;
                for (chunk_hash, xorb_hash, index) in &chunks {
                    chunk_stmt.execute(rusqlite::params![chunk_hash, xorb_hash, index])?;
                }
            }
            tx.commit()?;
            Ok(new_files)
        })
        .await
    }

    /// Resolve a chunk hash to the xorb that holds it and its index there.
    pub async fn lookup_chunk(&self, chunk_hash: &str) -> AppResult<Option<(String, u32)>> {
        let chunk_hash = chunk_hash.to_string();
        self.with(move |conn| {
            conn.query_row(
                "SELECT xorb_hash, chunk_index FROM chunks WHERE chunk_hash = ?1",
                [&chunk_hash],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .optional()
        })
        .await
    }

    /// Everything `/health` reports, in one round trip.
    ///
    /// All three numbers come out of the index, so the endpoint stays constant
    /// time as the store grows: the container health-checks it every five
    /// seconds and a recursive stat of every stored object would eventually
    /// time out on a large store even though request serving is healthy.
    pub async fn stats(&self) -> AppResult<IndexStats> {
        self.with(|conn| {
            let xorbs: i64 = conn.query_row("SELECT COUNT(*) FROM xorbs", [], |r| r.get(0))?;
            let files: i64 = conn.query_row("SELECT COUNT(*) FROM files", [], |r| r.get(0))?;
            let disk_bytes: i64 =
                conn.query_row("SELECT disk_bytes FROM store_stats WHERE id = 0", [], |r| {
                    r.get(0)
                })?;
            Ok(IndexStats {
                xorbs: xorbs as u64,
                files: files as u64,
                stored_bytes: disk_bytes as u64,
            })
        })
        .await
    }

    /// Row counts only.
    pub async fn counts(&self) -> AppResult<(u64, u64)> {
        let stats = self.stats().await?;
        Ok((stats.xorbs, stats.files))
    }
}

/// The index's own view of what the server holds.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct IndexStats {
    /// Number of stored xorbs.
    pub xorbs: u64,
    /// Number of registered files.
    pub files: u64,
    /// Bytes of xorb objects written to the store, accumulated as each xorb
    /// was indexed. Equal to the sum of the object file sizes under the object
    /// root for any index this schema created; it deliberately excludes orphan
    /// blobs left by a crash between the write and the index insert, which no
    /// reconstruction can reach.
    pub stored_bytes: u64,
}

#[cfg(test)]
mod tests {
    use tempfile::TempDir;
    use xetcas_contracts::v1::FileRecord;

    use super::{Index, INITIAL_SCHEMA, SCHEMA_VERSION};

    fn file(file_hash: &str, sha256: &str) -> FileRecord {
        FileRecord {
            file_hash: file_hash.to_string(),
            file_length: 0,
            sha256: sha256.to_string(),
            terms: vec![],
            verification_range_hashes: vec![],
            created_at: 0,
        }
    }

    #[tokio::test]
    async fn later_verified_sha256_upgrades_a_null_file_record_without_overwriting_trust() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("index.sqlite");
        let index = Index::open(&path).await.unwrap();
        let file_hash = "f".repeat(64);
        let verified_sha = "a".repeat(64);
        let conflicting_sha = "b".repeat(64);

        assert_eq!(
            index
                .put_files(vec![file(&file_hash, "")], vec![])
                .await
                .unwrap(),
            1
        );
        assert!(index.file_by_sha256(&verified_sha).await.unwrap().is_none());

        // `verify_sha256` has checked this SHA-256 before `put_files` is
        // called by the shard route. Persisting its accompanying record is
        // necessary because `get_file` decodes the record blob for downloads.
        assert_eq!(
            index
                .put_files(vec![file(&file_hash, &verified_sha)], vec![])
                .await
                .unwrap(),
            0
        );
        assert_eq!(
            index
                .file_by_sha256(&verified_sha)
                .await
                .unwrap()
                .expect("late verified SHA-256 is indexed")
                .sha256,
            verified_sha
        );

        // A later caller cannot replace an established digest. The route's
        // verification boundary rejects this impossible content conflict
        // before it reaches here; this direct test keeps the index invariant
        // true if another caller is ever added.
        index
            .put_files(vec![file(&file_hash, &conflicting_sha)], vec![])
            .await
            .unwrap();
        assert!(index
            .file_by_sha256(&conflicting_sha)
            .await
            .unwrap()
            .is_none());
        assert_eq!(
            index.get_file(&file_hash).await.unwrap().unwrap().sha256,
            verified_sha
        );
    }

    #[tokio::test]
    async fn new_and_existing_v1_indexes_open() {
        let dir = TempDir::new().unwrap();
        let new_path = dir.path().join("new.sqlite");
        Index::open(&new_path).await.unwrap();

        let version = rusqlite::Connection::open(&new_path)
            .unwrap()
            .query_row("SELECT version FROM schema_meta", [], |row| {
                row.get::<_, i64>(0)
            })
            .unwrap();
        assert_eq!(version, SCHEMA_VERSION, "new database records v1");

        let empty_path = dir.path().join("empty.sqlite");
        drop(rusqlite::Connection::open(&empty_path).unwrap());
        let empty = Index::open(&empty_path).await.unwrap();
        assert_eq!(empty.counts().await.unwrap(), (0, 0));

        let existing_path = dir.path().join("existing-v1.sqlite");
        let connection = rusqlite::Connection::open(&existing_path).unwrap();
        connection.execute_batch(INITIAL_SCHEMA).unwrap();
        connection
            .execute(
                "INSERT INTO schema_meta (version) VALUES (?1)",
                [SCHEMA_VERSION],
            )
            .unwrap();
        drop(connection);

        let existing = Index::open(&existing_path).await.unwrap();
        assert_eq!(existing.counts().await.unwrap(), (0, 0));
    }

    #[tokio::test]
    async fn unversioned_view_is_rejected_without_mutating_the_database() {
        let dir = TempDir::new().unwrap();
        // Only the literal `sqlite_` prefix is reserved for internal objects.
        for view_name in ["user_view", "sqlitex_view"] {
            let path = dir.path().join(format!("{view_name}.sqlite"));
            let connection = rusqlite::Connection::open(&path).unwrap();
            connection
                .execute_batch(&format!("CREATE VIEW {view_name} AS SELECT 42 AS value;"))
                .unwrap();
            drop(connection);
            let before = std::fs::read(&path).unwrap();

            let error = match Index::open(&path).await {
                Ok(_) => panic!("unversioned view-only database opened"),
                Err(error) => error,
            };
            assert!(error.to_string().contains("no schema version metadata"));
            assert_eq!(
                std::fs::read(&path).unwrap(),
                before,
                "rejection did not mutate the database"
            );

            let value: i64 = rusqlite::Connection::open(&path)
                .unwrap()
                .query_row(&format!("SELECT value FROM {view_name}"), [], |row| {
                    row.get(0)
                })
                .unwrap();
            assert_eq!(value, 42, "existing view remains usable");
        }
    }

    #[tokio::test]
    async fn unknown_schema_version_is_rejected_without_mutating_the_database() {
        let dir = TempDir::new().unwrap();
        let path = dir.path().join("future.sqlite");
        let connection = rusqlite::Connection::open(&path).unwrap();
        connection
            .execute_batch("CREATE TABLE schema_meta (version INTEGER NOT NULL); INSERT INTO schema_meta VALUES (2);")
            .unwrap();
        drop(connection);
        let before = std::fs::read(&path).unwrap();

        let error = match Index::open(&path).await {
            Ok(_) => panic!("future schema opened"),
            Err(error) => error,
        };
        assert!(error
            .to_string()
            .contains("unsupported index schema version 2"));
        assert_eq!(
            std::fs::read(&path).unwrap(),
            before,
            "rejection did not mutate the database"
        );
    }
}
