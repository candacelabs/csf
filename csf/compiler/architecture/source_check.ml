(** Connect a resolved declaration to the files that actually exist.

    Validation has already checked the declaration graph. This pass asks a
    different question: were the claimed implementations inspected, and do
    their selected Go syntax nodes respect their declared process roles?
    For example, merely declaring [source "go/services/x"] cannot cover a
    component if traversal found no production Go file underneath that path.
    No service, subprocess or referenced test is executed here. *)
open Model
module Paths = Set.Make (String)

module Policy = struct
  type finding = Path | Coverage | Io
  let code = function
    | Path -> "CSF_PATH"
    | Coverage -> "CSF_SOURCE_COVERAGE"
    | Io -> "CSF_IO"
  (* Skip these child directories during traversal. An explicitly selected
     scan root is still visited; coverage always uses the resulting inventory. *)
  let excluded_directories = [".git"; ".cache"; "node_modules"]
end

type context = { root : string; errors : diagnostic list ref }
let inside parent path = path = parent || String.starts_with ~prefix:(parent ^ "/") path
let report context at finding message =
  context.errors := {at; code = Policy.code finding; message} :: !(context.errors)

(* Convert per-file failures into findings and continue collecting independent
   findings. [None] means inspection failed; it must never count as coverage. *)
let protect context at operation =
  try Some (operation ()) with
  | Invalid_argument message -> report context at Policy.Path message; None
  | Sys_error message -> report context at Policy.Io message; None
  | Unix.Unix_error (error, operation, path) ->
      report context at Policy.Io (operation ^ " " ^ path ^ ": " ^ Unix.error_message error); None

(* Reuse the shared check for every path ancestor before touching the file.
   Checking only the final filename would miss a symlink in its parent path.
   The checkout must remain stable during inspection; this is not a snapshot. *)
let with_path context at path inspect =
  protect context at (fun () -> inspect (Checker.contained_path context.root path))

let require_file context at path =
  ignore (with_path context at path (fun full ->
    if (Unix.lstat full).st_kind <> Unix.S_REG then
      report context at Policy.Path ("expected regular file: " ^ path)))

(* A test reference is only an existing regular file, not a passing result.
   Planned components need not have implementation files yet. *)
let references context (model : architecture) =
  List.iter (fun (process : process) ->
    Option.iter (require_file context process.process_at) process.entrypoint) model.processes;
  List.iter (fun (component : component) ->
    if component.state = Existing then
      Option.iter (fun path -> ignore (with_path context component.component_at path ignore)) component.source;
    match component.verification with
    | Pending -> ()
    | Test_reference path -> require_file context component.component_at path) model.components

(* Return a deterministic, deduplicated inventory. Overlapping scan roots must
   not duplicate file-policy findings; traversal errors can still repeat.
   Sorting also makes continuous integration output reproducible. *)
let files context roots =
  let found = ref Paths.empty in
  let rec visit at path =
    ignore (with_path context at path (fun full ->
      match (Unix.lstat full).st_kind with
      | Unix.S_DIR ->
          Sys.readdir full |> Array.to_list |> List.sort String.compare |> List.iter (fun name ->
            if not (List.mem name Policy.excluded_directories) then visit at (Filename.concat path name))
      | Unix.S_REG -> found := Paths.add path !found
      | _ -> report context at Policy.Path ("expected regular source file: " ^ path))) in
  List.iter (fun (source : source_root) -> visit source.path_at source.path) roots;
  Paths.elements !found

(* Use the visited inventory, not declared path prefixes: otherwise a component
   under a skipped subtree, or represented by tests alone, could appear checked. *)
let coverage context (model : architecture) selected =
  let implementation = List.filter (fun path -> Go_policy.is_source path && not (Go_policy.is_test path)) selected in
  if model.scan_roots = [] then
    report context model.at Policy.Coverage "at least one scan root is required";
  List.iter (fun (host : process) -> if host.kind = Go then begin
    Option.iter (fun path -> if not (List.mem path implementation) then
      report context host.process_at Policy.Coverage ("Go entrypoint was not selected for inspection: " ^ path)) host.entrypoint;
    List.iter (fun (component : component) ->
      if component.state = Existing && component.process = host.process_id then
        Option.iter (fun path -> if not (List.exists (inside path) implementation) then
          report context component.component_at Policy.Coverage
            ("component source has no selected Go implementation: " ^ path)) component.source) model.components
  end) model.processes

(* A gateway may name one file or a directory. Pass that declaration-derived
   role to the syntax checker; finding an exec call must not grant the file
   gateway status. Helpers in the entrypoint package may say [package main],
   but only the declared entrypoint may own the selected listener functions. *)
let go_sources context (model : architecture) selected =
  let entrypoints = List.filter_map (fun (process : process) -> process.entrypoint) model.processes in
  let gateways = model.components
    |> List.filter (fun (component : component) -> component.role = Gateway && component.state = Existing)
    |> List.filter_map (fun (component : component) -> component.source) in
  selected |> List.filter Go_policy.is_source |> List.iter (fun path ->
    ignore (with_path context {file = path; line = 1; column = 1} path (fun full ->
      let source = In_channel.with_open_bin full In_channel.input_all in
      let findings = Go_policy.inspect ~entrypoint:(List.mem path entrypoints)
        ~entrypoint_package:(List.exists (fun entry -> Filename.dirname entry = Filename.dirname path) entrypoints)
        ~gateway:(List.exists (fun source -> inside source path) gateways)
        ~test:(Go_policy.is_test path) path source in
      context.errors := List.rev_append findings !(context.errors))))

(* Read only the policy's header window. This enforces naming/disclaimers;
   generated byte-for-byte reproducibility belongs to the owning generator. *)
let generated_sources context (model : architecture) =
  files context model.generated_roots
  |> List.filter (fun path -> Generated_policy.checked_extension (Filename.extension path))
  |> List.iter (fun path ->
    ignore (with_path context {file = path; line = 1; column = 1} path (fun full ->
      let source = In_channel.with_open_bin full (fun channel ->
        let buffer = Bytes.create Generated_policy.header_prefix_bytes in
        let count = input channel buffer 0 (Bytes.length buffer) in Bytes.sub_string buffer 0 count) in
      context.errors := List.rev_append (Generated_policy.inspect ~path ~source) !(context.errors))))

(* Each call owns its diagnostic accumulator. [ref] permits local collection;
   reversing at the boundary returns findings in traversal order, without
   leaking mutable state into the public result. *)
let check ~root (resolved : resolved) =
  let context = {root; errors = ref []} in
  (match protect context resolved.architecture.at (fun () -> Unix.realpath root) with
  | None -> ()
  | Some root ->
      let context = {context with root} in
      references context resolved.architecture;
      let selected = files context resolved.architecture.scan_roots in
      coverage context resolved.architecture selected;
      go_sources context resolved.architecture selected;
      generated_sources context resolved.architecture);
  List.rev !(context.errors)
