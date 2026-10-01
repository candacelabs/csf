exception Error of string

type totals = {
  generated_lines : int;
  non_generated_lines : int;
  total_lines : int;
  generated_percent : float option;
  non_generated_percent : float option;
}

type source_file = { path : string; language : string; generated : bool; lines : int }
type documentation_file = { path : string; totals : totals }
type report = {
  code : totals;
  generated_files : int;
  non_generated_files : int;
  excluded_files : int;
  modified_files : string list;
  by_language : (string * totals) list;
  files : source_file list;
  documentation : totals;
  documentation_files : documentation_file list;
}

val calculate : root:string -> Yojson.Safe.t list -> report
val read_manifest : string -> Yojson.Safe.t list
val to_json : report -> Yojson.Safe.t
