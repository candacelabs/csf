(** Corpus readers. Each reads its source in place; nothing is copied into
    the repository or a cache, and transcripts are never written anywhere. *)

(** One JSON value per non-blank line with its 1-based line number, read
    lazily: harness event logs and transcripts under [~/.claude] alike. A
    malformed line fails with its path and line. *)
val jsonl : string -> (int * Yojson.Safe.t) Seq.t

(** [<state>/<assignment>/events.jsonl], the harness event log. *)
val events : state:string -> assignment:string -> string

(** [gh issue view] as JSON: title, body, state and comments. *)
val issue : repository:string -> int -> Yojson.Safe.t

(** [gh pr view] as JSON: title, body, state and merge commit. *)
val pull_request : repository:string -> int -> Yojson.Safe.t

(** A repository file's contents at a revision ([git show REV:PATH]). *)
val file_at : checkout:string -> revision:string -> string -> string

(** Unix seconds of an RFC 3339 UTC timestamp; fractional seconds are
    dropped. *)
val seconds : string -> int
