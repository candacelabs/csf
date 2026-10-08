(** The compiler's one git child process. [output ~root arguments] runs
    [git -C root arguments] with no shell and returns its standard output, or
    [None] when git is missing, fails to start or exits non-zero. Standard
    error passes through to the caller's. *)
val output : root:string -> string list -> string option
