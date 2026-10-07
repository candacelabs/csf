(** Inspect the selected repository sources for a semantically resolved model.
    Filesystem failures become diagnostics; [check] does not print, exit, read
    environment variables or modify files.

    Coverage is measured against visited production Go files, after traversal
    exclusions. Every relative path component is checked for symlinks. This is
    a checkout check, not an atomic snapshot or a defense against concurrent
    hostile filesystem mutation. *)
val check : root:string -> Model.resolved -> Model.diagnostic list

(** The tracked files under [root], from [git ls-files]. This is the one place
    this module runs a child process; a missing or failing git yields an empty
    list, so the caller reports no census offense rather than a false one. *)
val tracked : root:string -> string list
