let jsonl path =
  let rec next channel line () = match In_channel.input_line channel with
    | None -> In_channel.close channel; Seq.Nil
    | Some text when String.trim text = "" -> next channel (line + 1) ()
    | Some text -> (match Yojson.Safe.from_string text with
      | json -> Seq.Cons ((line, json), next channel (line + 1))
      | exception Yojson.Json_error message ->
          In_channel.close channel;
          failwith (Printf.sprintf "%s:%d: %s" path line message)) in
  fun () -> next (In_channel.open_text path) 1 ()

let events ~state ~assignment = Filename.concat (Filename.concat state assignment) "events.jsonl"

let output command =
  let channel = Unix.open_process_args_in command.(0) command in
  let text = In_channel.input_all channel in
  match Unix.close_process_in channel with
  | Unix.WEXITED 0 -> text
  | _ -> failwith (String.concat " " (Array.to_list command) ^ ": failed")

let issue ~repository number = Yojson.Safe.from_string (output [|
  "gh"; "issue"; "view"; string_of_int number; "--repo"; repository; "--json"; "title,body,state,comments" |])

let pull_request ~repository number = Yojson.Safe.from_string (output [|
  "gh"; "pr"; "view"; string_of_int number; "--repo"; repository; "--json"; "title,body,state,mergeCommit" |])

let file_at ~checkout ~revision path = output [| "git"; "-C"; checkout; "show"; revision ^ ":" ^ path |]

(* Days from the civil date, proleptic Gregorian (Hinnant's algorithm). *)
let seconds text =
  Scanf.sscanf text "%4d-%2d-%2dT%2d:%2d:%2d" (fun year month day hour minute second ->
    let year = if month <= 2 then year - 1 else year in
    let era = (if year >= 0 then year else year - 399) / 400 in
    let of_era = year - era * 400 in
    let of_year = (153 * (if month > 2 then month - 3 else month + 9) + 2) / 5 + day - 1 in
    let days = era * 146097 + of_era * 365 + of_era / 4 - of_era / 100 + of_year - 719468 in
    ((days * 24 + hour) * 60 + minute) * 60 + second)
