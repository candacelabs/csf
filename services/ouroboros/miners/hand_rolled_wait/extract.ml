(* HAND-ROLLED-WAIT (#371), the extractor: every command an actor ran
   through the executor's shell or watch tool, from a harness event log or
   an executor transcript, to facts about its shape. Only the command's
   shape is read: whether it loops, whether it sleeps between polls, whether
   it ran in the executor's watch tool and whether it is CSF's own await. *)
open Contract

(* A record without the key, or a value that is not an object, has no member:
   a harness event carries no message and a transcript block no input. *)
let member name json = match json with `Assoc _ -> Yojson.Safe.Util.member name json | _ -> `Null
let field name json = match member name json with `String text -> Some text | _ -> None

let shell_tool = "Bash"
let watch_tool = "Monitor"

let contains ~sub text =
  let width = String.length sub in
  let rec at index = index + width <= String.length text && (String.sub text index width = sub || at (index + 1)) in
  at 0

(* The terminator a line's here-document names, as in cat <<'EOF'; a
   here-string (<<<) names none. *)
let heredoc_terminator line =
  let length = String.length line in
  let rec find index =
    if index + 1 >= length then None
    else if line.[index] = '<' && line.[index + 1] = '<' then
      if index + 2 < length && line.[index + 2] = '<' then None else word (index + 2)
    else find (index + 1)
  and word index =
    let skip = function '-' | ' ' | '\'' | '"' -> true | _ -> false in
    let start = ref index in
    while !start < length && skip line.[!start] do incr start done;
    let stop = ref !start in
    while !stop < length && (match line.[!stop] with 'A'..'Z' | 'a'..'z' | '_' -> true | _ -> false) do incr stop done;
    if !stop > !start then Some (String.sub line !start (!stop - !start)) else None in
  find 0

(* A here-document's body is data, not commands: a pull request body that
   quotes a loop is not a loop. *)
let strip_heredocs command =
  let rec keep terminator lines = match terminator, lines with
    | _, [] -> []
    | Some word, line :: rest -> if String.trim line = word then keep None rest else keep terminator rest
    | None, line :: rest -> line :: keep (heredoc_terminator line) rest in
  String.concat "\n" (keep None (String.split_on_char '\n' command))

(* How a simple command was started: in the background after a lone &, or
   in the foreground after any other separator. *)
type start = Foreground | Background

(* The simple commands of a script, as words, each with how it started.
   Quoting is not parsed, so a script handed to bash -c is read too; a word's
   leading quotes are dropped, so its first command reads as one. *)
let segments command =
  let separators = [';'; '\n'; '|'; '('; ')'; '`'; '{'; '}'] in
  let unquoted word =
    let start = ref 0 in
    while !start < String.length word && (word.[!start] = '\'' || word.[!start] = '"') do incr start done;
    String.sub word !start (String.length word - !start) in
  let words text = String.split_on_char ' ' (String.map (fun char -> if char = '\t' then ' ' else char) text)
    |> List.map unquoted |> List.filter (fun word -> word <> "") in
  let length = String.length command in
  let rec scan index start from found =
    let close () = (from, words (String.sub command start (index - start))) :: found in
    if index >= length then List.rev (close ())
    else match command.[index] with
      | '&' when index + 1 < length && command.[index + 1] = '&' -> scan (index + 2) (index + 2) Foreground (close ())
      | '&' -> scan (index + 1) (index + 1) Background (close ())
      | char when List.mem char separators -> scan (index + 1) (index + 1) Foreground (close ())
      | _ -> scan (index + 1) start from found in
  scan 0 0 Foreground []

let keywords = ["do"; "then"; "else"; "command"; "!"]
let rec command_words = function word :: rest when List.mem word keywords -> command_words rest | words -> words
let numeric word = word <> "" && (match word.[0] with '0'..'9' -> true | _ -> false)

(* A sleep in the foreground of the script. A sleep straight after a lone &
   paces background jobs; it is the subject of its loop, not a wait. *)
let sleeps segments = List.exists (fun (start, words) -> start = Foreground && match command_words words with
  | "sleep" :: duration :: _ -> numeric duration
  | _ -> false) segments

(* A while, until or for ... in loop with its do. The loop may open after
   a wrapper, as in timeout 600 bash -c 'until ...'. *)
let loops segments =
  let rec opening = function
    | ("while" | "until") :: _ -> true
    | "for" :: _ :: "in" :: _ -> true
    | _ :: rest -> opening rest
    | [] -> false in
  let opens = List.exists (fun (_, words) -> opening words) segments in
  opens && List.exists (fun (_, words) -> match words with "do" :: _ -> true | _ -> false) segments

(* One tool call: the block's id, the tool's name and the command it ran.
   Named so a call is not a bare triple. *)
type tool_call = { id : string; name : string; command : string }

(* The tool calls of one record: an executor transcript's message, or the
   harness event that carries one. *)
let tool_calls json =
  let content message = match member "content" message with `List blocks -> blocks | _ -> [] in
  let messages = [member "message" json; (match member "event" json with `Assoc _ as event -> member "message" event | _ -> `Null)] in
  List.concat_map content messages
  |> List.filter_map (fun block ->
    match field "type" block, field "id" block, field "name" block, field "command" (member "input" block) with
    | Some "tool_use", Some id, Some name, Some command when name = shell_tool || name = watch_tool -> Some { id; name; command }
    | _ -> None)

(* One log's records, up to a malformed line: a live log may end in a line
   still being written. *)
let records path =
  let rec read sequence found = match sequence () with
    | Seq.Nil -> List.rev found
    | Seq.Cons (record, rest) -> read rest (record :: found)
    | exception Failure _ -> List.rev found in
  read (Corpus.jsonl path) []

let extract_log path =
  List.concat_map (fun (line, json) ->
    List.concat_map (fun call ->
      let fact relation = { relation; args = [Text call.id]; span = { source = path; line } } in
      let shape = segments (strip_heredocs call.command) in
      List.concat [
        [fact "call"];
        (if loops shape then [fact "loops"] else []);
        (if sleeps shape then [fact "sleeps"] else []);
        (if call.name = watch_tool then [fact "watches"] else []);
        (if contains ~sub:"csf await" call.command then [fact "awaits"] else []);
      ]) (tool_calls json)) (records path)

(* An item is one log, or a directory of them: an executor's transcripts. *)
let extract path =
  if Sys.is_directory path then
    Sys.readdir path |> Array.to_list |> List.filter (fun name -> Filename.check_suffix name ".jsonl")
    |> List.sort compare |> List.concat_map (fun name -> extract_log (Filename.concat path name))
  else extract_log path

let miner = {
  name = "hand_rolled_wait";
  package = "services/ouroboros/miners/hand_rolled_wait";
  rules = Rules_cgen.source;
  verdicts = [{ name = "hand_rolled_wait"; arity = 1; severity = S3 }];
  (* A wait outside CSF's await is an offense for any harness user. *)
  scope = Generic;
  extract;
}
