(* NO-SELF-MERGE (ticket #249), the extractor. Detects when a PR authored
   by a session is merged by the same session without an independent verdict. *)
open Contract

let field name json = match Yojson.Safe.Util.member name json with `String text -> Some text | _ -> None
let pr_number json = match Yojson.Safe.Util.member "number" json with `Int n -> Some (Text (string_of_int n)) | _ -> None

(* Extracts CSF-Session from a dedicated field or from PR body trailer. *)
let session_from_field json field_name =
  field field_name json |> Option.map (fun s -> Text s)

let session_from_text text =
  (* Look for CSF-Session: ... pattern in text *)
  let lines = String.split_on_char '\n' text in
  List.find_map (fun line ->
    if String.starts_with ~prefix:"CSF-Session:" line then
      let session = String.trim (String.sub line 12 (String.length line - 12)) in
      if String.length session > 0 then Some (Text session) else None
    else None) lines

(* One PR JSON file to facts: pr_author, merging_session, has_independent_verdict. *)
let extract path =
  let lines = List.of_seq (Corpus.jsonl path) in
  let fact relation args line_num = { relation; args; span = { source = path; line = line_num } } in
  match lines with
  | [] -> []
  | (pr_line, pr_json) :: rest ->
      match pr_number pr_json with
      | None -> []
      | Some pr_id ->
          (* Get author session from author_session field or body *)
          let author =
            match session_from_field pr_json "author_session" with
            | Some s -> Some s
            | None -> session_from_text (Option.value (field "body" pr_json) ~default:"")
          in
          let is_merged = field "merge_commit_sha" pr_json <> None in

          (* Get merging session from merge event *)
          let merge_session =
            if is_merged then
              List.find_map (fun (_, event_json) ->
                session_from_field event_json "merge_session") rest
            else None
          in

          (* Check if there's an independent verdict from a different session *)
          let has_verdict =
            List.exists (fun (_, comment_json) ->
              match session_from_field comment_json "reviewer_session", author with
              | Some (Text rev_session), Some (Text auth_session) -> rev_session <> auth_session
              | _ -> false) rest
          in

          List.concat [
            (match author with Some auth -> [fact "pr_author" [pr_id; auth] pr_line] | None -> []);
            (match merge_session with Some sess -> [fact "pr_merged_by" [pr_id; sess] pr_line] | None -> []);
            (if has_verdict then [fact "has_independent_verdict" [pr_id] pr_line] else []);
          ]

let miner = {
  name = "no_self_merge";
  package = "services/ouroboros/miners/no_self_merge";
  rules = Rules_cgen.source;
  verdicts = [{ name = "refused"; arity = 1; severity = S0 }];
  (* Who may merge whose pull request without an independent verdict is an
     operator's ruling (#249), not a defect every harness user shares, so its
     findings never leave the tenant. *)
  scope = Tenant;
  extract;
}
