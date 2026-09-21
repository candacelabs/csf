(** File conventions and a bounded syntax policy over selected Go source.
    Import selectors are matched by alias spelling, not Go type information;
    local shadowing may therefore produce conservative findings. Go compilation
    remains responsible for language validity and build-constraint selection. *)
val is_source : string -> bool
val is_test : string -> bool

(** Flags describe the file's declared role, not permissions inferred from its
    contents. A declared entrypoint must contain package main and a main
    function. [entrypoint_package] admits helper files in the same package;
    it does not grant them ownership of entrypoint-only functions. [gateway] admits
    selected child-process references. [test] admits the documented fixture
    exceptions. The final two arguments are the diagnostic path and source
    contents. Results retain source locations; an empty list means no finding
    under this bounded policy, not absence of every possible process effect. *)
val inspect : entrypoint:bool -> entrypoint_package:bool -> gateway:bool ->
  test:bool -> string -> string -> Model.diagnostic list
