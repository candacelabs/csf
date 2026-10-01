(** Inspect the selected repository sources for a semantically resolved model.
    Filesystem failures become diagnostics; this function does not print, exit,
    read environment variables, launch processes or modify files.

    Coverage is measured against visited production Go files, after traversal
    exclusions. Every relative path component is checked for symlinks. This is
    a checkout check, not an atomic snapshot or a defense against concurrent
    hostile filesystem mutation. *)
val check : root:string -> Model.resolved -> Model.diagnostic list
