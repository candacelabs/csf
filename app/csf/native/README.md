# CSF native payload receipts

This directory owns the portable receipt format for a native CSF payload. It
does not install packages, configure the host, or start services. The
standalone runtime remains the Compose project operated by `candace csf`.

`receipt-rs` replaces the former Python receipt writer. It records the selected
component metadata plus the mode and SHA-256 digest of every regular file and
safe relative symlink below a payload directory. Output is sorted and rendered
as two-space JSON in `PAYLOAD_RECEIPT.json`.

Call the checked-in Rust implementation through the stable wrapper:

```bash
candace/app/csf/native/write-receipt.sh \
  --component csf \
  --version source \
  --target debian-12-linux-amd64 \
  --source git-tree:0123456789abcdef \
  --source-sha256 0123456789abcdef \
  --payload /path/to/payload
```

The command rejects an empty payload and absolute, dangling, cyclic, or
escaping symlinks. It requires Cargo with Rust 1.85 or newer and uses the
locked dependency graph.
