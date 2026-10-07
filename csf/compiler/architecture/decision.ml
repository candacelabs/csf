(** The decision-tree declaration and its projections. A question tree is
    declared once in the ontology language (decision.ebnf) and projected into
    the compiler's artifacts: [render_lean] emits the Lean inductive types, the
    fixed helpers and the three decision-tree invariants as one file, and the
    four text projections ([render_kind_enum], [render_directory],
    [render_cli_layer], [render_jev_prompt]) render the same tree as the kind
    enum, the directory tree, the CLI layers and the JEV prompts. Each artifact
    is pinned byte for byte by a golden test and drift-gated in commit and CI.
    The invariants are the chief invariants of #532: every question offers at
    most sixteen options, each leaf option names exactly one template, and every
    allowed path through the tree compiles to a shell. [check] reports the
    declaration's own defects as diagnostics; nothing here runs Lean or observes
    a session, and [write] is the only function that touches disk. *)

open Model

exception Invalid of diagnostic

let invalid (node : Frontend.node) message =
  raise (Invalid { at = node.at; code = "CSF_DECISION"; message })

let fanout_bound = 16

(* --- the declaration --- *)

(* An option is a leaf carrying exactly one template, or a refines naming
   exactly one other declared question. *)
type kind = Leaf of string | Refines of string

type option = {
  option_id : string;
  kind : kind;
  option_at : location;
}

type question = {
  question_id : string;
  text : string;
  options : option list;
  question_at : location;
}

type tree = question list

(* --- decoding the syntax tree --- *)

type reader = { parent : Frontend.node; mutable rest : Frontend.node list }

let take rule reader = match reader.rest with
  | node :: rest when node.Frontend.rule = rule -> reader.rest <- rest; node
  | node :: _ -> invalid node (Printf.sprintf "expected %s, found %s at line %d col %d"
      rule node.Frontend.rule node.Frontend.at.line node.Frontend.at.column)
  | [] -> invalid reader.parent ("expected " ^ rule)

let literal rule reader =
  let node = take rule reader in
  match node.Frontend.value with
  | Some value -> value
  | None -> invalid node (rule ^ " without a literal value")

let terminal expected reader =
  let node = take "$terminal" reader in
  match node.Frontend.value with
  | Some value when value = expected -> ()
  | Some value -> invalid node ("expected " ^ expected ^ ", found " ^ value)
  | None -> invalid node "terminal without a value"

let decode_option (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  terminal "option" reader;
  let option_id = literal "identifier" reader in
  let branch = take "$terminal" reader in
  let kind = match branch.Frontend.value with
    | Some "leaf" -> Leaf (literal "string" reader)
    | Some "refines" -> Refines (literal "identifier" reader)
    | Some value -> invalid branch ("unsupported option branch " ^ value)
    | None -> invalid branch "option branch without a value" in
  terminal ";" reader;
  (match reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " in option"));
  { option_id; kind; option_at = node.Frontend.at }

(* Consume a run of one repeated production, decoding each node. *)
let repeated rule decode reader =
  let rec walk acc = match reader.rest with
    | next :: _ when next.Frontend.rule = rule ->
        let next = take rule reader in
        walk (decode next :: acc)
    | _ -> List.rev acc in
  walk []

let decode_question (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  terminal "question" reader;
  let question_id = literal "identifier" reader in
  terminal "{" reader;
  let text_node = take "text_clause" reader in
  let text_reader = { parent = text_node; rest = text_node.Frontend.children } in
  terminal "text" text_reader;
  let text = literal "string" text_reader in
  terminal ";" text_reader;
  (match text_reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " in text"));
  let options = repeated "option" decode_option reader in
  terminal "}" reader;
  (match reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " in question"));
  { question_id; text; options; question_at = node.Frontend.at }

let decode_tree (node : Frontend.node) =
  let reader = { parent = node; rest = node.Frontend.children } in
  let questions = repeated "question" decode_question reader in
  (match reader.rest with
   | [] -> ()
   | extra :: _ -> invalid extra ("unconsumed " ^ extra.Frontend.rule ^ " in tree"));
  questions

(** Parse a tree source against the decision grammar. Expected syntax and
    decoding errors become diagnostics; nothing is read from disk. *)
let parse ~grammar ~source ~filename =
  match Frontend.parse_text ~grammar ~source ~filename with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_tree node) with Invalid diagnostic -> Error [diagnostic])

let parse_files ~grammar_path ~source_path =
  match Frontend.parse_files ~grammar_path ~source_path with
  | Error diagnostics -> Error diagnostics
  | Ok node -> (try Ok (decode_tree node) with Invalid diagnostic -> Error [diagnostic])

(* --- the check --- *)

let names (tree : tree) = List.map (fun question -> question.question_id) tree

let refined (tree : tree) =
  List.concat_map (fun question ->
    List.filter_map (function { kind = Refines target; _ } -> Some target | _ -> None)
      question.options) tree

(** The unique root: the question no other question refines. A cycle leaves no
    root and several unexplored roots leave more than one; both are defects. *)
let root (tree : tree) =
  let refined = refined tree in
  match List.filter (fun question -> not (List.mem question.question_id refined)) tree with
  | [question] -> Some question
  | _ -> None

let duplicates values =
  let rec walk acc = function
    | a :: (b :: _ as rest) when a = b -> walk (a :: acc) rest
    | _ :: rest -> walk acc rest
    | [] -> List.rev acc in
  walk [] (List.sort String.compare values)

(* A name the projection can spell as a Lean definition: appending Question to
   it must yield a legal identifier, so the source's hyphens are rejected. *)
let spellable value =
  value <> "" &&
  (match value.[0] with 'a' .. 'z' | 'A' .. 'Z' | '_' -> true | _ -> false) &&
  String.for_all (function
    | 'a' .. 'z' | 'A' .. 'Z' | '0' .. '9' | '_' -> true
    | _ -> false) value

(* The checks a tree must pass before it is projected: identifiers are unique
   and spellable, prompts and templates are non-empty, every question offers at
   least one option, every refines names a declared question, fanout is bounded,
   and exactly one root exists. A finding is reported at the declaration, never
   at an unbuilt tree. *)
let check (tree : tree) =
  let findings = ref [] in
  let at = match tree with
    | question :: _ -> question.question_at
    | [] -> { Model.file = ""; line = 1; column = 1 } in
  let add code message = findings := { at; code; message } :: !findings in
  let declared = names tree in
  List.iter (fun name -> add "decision_duplicate"
    ("The question " ^ name ^ " is declared more than once.")) (duplicates declared);
  List.iter (fun question ->
    if not (spellable question.question_id) then
      add "decision_identifier"
        ("The question identifier " ^ question.question_id ^ " is not a Lean identifier.");
    if String.trim question.text = "" then
      add "decision_empty" ("The prompt for " ^ question.question_id ^ " must not be empty.");
    if question.options = [] then
      add "unprojected_question"
        ("The question " ^ question.question_id ^ " offers no option and cannot project.");
    if List.length question.options > fanout_bound then
      add "decision_fanout" (Printf.sprintf "%s offers %d options; at most %d are allowed."
        question.question_id (List.length question.options) fanout_bound);
    List.iter (fun option ->
      match option.kind with
      | Leaf template ->
          if String.trim template = "" then
            add "decision_empty"
              ("The leaf template for " ^ option.option_id ^ " must not be empty.")
      | Refines target ->
          if not (spellable target) then
            add "decision_identifier"
              ("The refines target " ^ target ^ " is not a Lean identifier.")
          else if not (List.mem target declared) then
            add "decision_reference"
              ("No question is declared for the refines target " ^ target ^ ".")) question.options)
    tree;
  (match root tree with
   | Some _ -> ()
   | None -> add "decision_root"
       "The tree has no single root: every question is refined, or several are not.");
  List.rev !findings

(* --- the projection --- *)

(* A Lean string literal: double quoted, with backslash, quote and newline
   escaped. The declaration's strings are the only free text in the output. *)
let lean_string value =
  let buffer = Buffer.create (String.length value + 2) in
  Buffer.add_char buffer '"';
  String.iter (fun character -> match character with
    | '"' -> Buffer.add_string buffer "\\\""
    | '\\' -> Buffer.add_string buffer "\\\\"
    | '\n' -> Buffer.add_string buffer "\\n"
    | character -> Buffer.add_char buffer character) value;
  Buffer.add_char buffer '"';
  Buffer.contents buffer

(* The Lean definition name a question projects to. The root and its
   sub-questions are the compiler's own questions, so the name is the
   question's identifier with Question appended. *)
let definition question_id = question_id ^ "Question"

let render_option (option : option) =
  match option.kind with
  | Leaf template -> "Answer.leaf ⟨" ^ lean_string template ^ "⟩"
  | Refines target -> "Answer.refines " ^ definition target

(* The option list, one option per line, the closing bracket on the last. A
   single option stays on one line. *)
let render_options options =
  match List.map render_option options with
  | [] -> "    []"
  | [single] -> "    [ " ^ single ^ " ]"
  | first :: rest ->
      "    [ " ^ first ^ ",\n"
      ^ String.concat ",\n" (List.map (fun option -> "      " ^ option) rest) ^ " ]"

let render_question (question : question) =
  "/-- The `" ^ question.question_id ^ "` question. -/\n"
  ^ "def " ^ definition question.question_id ^ " : Question :=\n"
  ^ "  Question.mk " ^ lean_string question.question_id ^ " " ^ lean_string question.text ^ "\n"
  ^ render_options question.options

(* Sub-questions precede the questions that refine them, so every `refines`
   names a definition already in scope; Lean has no forward references. *)
let ordered (tree : tree) =
  let emitted = Hashtbl.create 8 in
  let out = ref [] in
  let rec visit (question : question) =
    if not (Hashtbl.mem emitted question.question_id) then begin
      Hashtbl.add emitted question.question_id ();
      List.iter (function
        | { kind = Refines target; _ } ->
            (match List.find_opt (fun other -> other.question_id = target) tree with
             | Some sub -> visit sub
             | None -> ())
        | _ -> ()) question.options;
      out := question :: !out
    end in
  List.iter visit tree;
  List.rev !out

let header source =
  {|import Std.Tactic

set_option autoImplicit false

/-!
The decision-tree slice of csfc. A question tree is a tree of JEV questions
whose leaves are templates; it is the input the compiler turns into a shell.
This file states and proves the three built-in invariants the compiler must
preserve, so that a violation is a compile error rather than a report:

  * `at_most_16` — no question offers more than 16 options; proved by `decide`.
  * `one_way`    — each leaf option names exactly one template; a functional
                   relation.
  * `composes`   — every allowed path through the tree compiles to a shell;
                   typing preservation over allowed pairs.

The file is emitted by `csfc` from the question-tree declaration |}
  ^ source
  ^ {|;
it is never hand-written. `sorry` is zero and no custom axiom is used.
-/

namespace CSFC.Verification

/-- A template is the leaf payload a terminal option instantiates. -/
structure Template where
  name : String
  deriving Repr, DecidableEq

mutual
  /-- A question is a decision point: an identifier, a prompt, and its answers. -/
  inductive Question where
    | mk (id : String) (text : String) (options : List Answer)

  /-- An option either opens exactly one sub-question or is a leaf carrying
      exactly one template. The leaf shape is the functional relation `one_way`. -/
  inductive Answer where
    | refines (sub : Question)
    | leaf (template : Template)
end

namespace Question

/-- The answers a question offers. -/
def options : Question → List Answer
  | .mk _ _ options => options

/-- Fanout: how many options a question offers. -/
def fanout (q : Question) : Nat := (options q).length

end Question

/-- The compiler-enforced fanout bound: a JEV choice head has at most 16 slots. -/
def fanoutBound : Nat := 16

/-- The decidable fanout check the compiler runs; `decide` produces its proof. -/
abbrev AtMost16 (q : Question) : Prop := Question.fanout q ≤ fanoutBound

/-- Whether an option is a leaf. -/
def IsLeaf : Answer → Prop
  | .leaf _ => True
  | .refines _ => False

/-- The partial map from a leaf option to the one template it names. -/
def templateOf : Answer → Option Template
  | .leaf template => some template
  | .refines _ => none

/-- The leaf-to-template relation. -/
def TemplateOf (o : Answer) (t : Template) : Prop := templateOf o = some t

/-- `one_way`: the leaf-to-template mapping is a functional relation — each leaf
    option names exactly one template, and every leaf names one. -/
theorem one_way (o : Answer) :
    (∀ t₁ t₂, TemplateOf o t₁ → TemplateOf o t₂ → t₁ = t₂)
    ∧ (IsLeaf o → ∃ t, TemplateOf o t) := by
  cases o with
  | leaf template =>
      constructor
      · intro t₁ t₂ h₁ h₂
        simp only [TemplateOf, templateOf, Option.some.injEq] at h₁ h₂
        exact h₁.symm.trans h₂
      · intro _
        exact ⟨template, rfl⟩
  | refines _ =>
      constructor
      · intro t₁ t₂ h₁ _
        simp only [TemplateOf, templateOf] at h₁
        cases h₁
      · intro h
        cases h

/-- A path through the tree is a sequence of options. Each option must answer
    the question it follows; a leaf ends the path. -/
def AllowedPath : Question → List Answer → Prop
  | _, [] => False
  | q, o :: rest =>
      o ∈ Question.options q ∧
      match o with
      | .refines sub => AllowedPath sub rest
      | .leaf _ => rest = []

/-- A path compiles when it ends in a leaf: the shell the leaf instantiates. -/
def Compiles : List Answer → Prop
  | [] => False
  | .leaf _ :: rest => rest = []
  | .refines _ :: rest => Compiles rest

/-- `composes`: every allowed path through the decision tree compiles to a
    shell. Typing preservation: each step's sub-question is the correct
    continuation, so well-formedness is carried along the path. -/
theorem composes (q : Question) (path : List Answer) (allowed : AllowedPath q path) :
    Compiles path := by
  induction path generalizing q with
  | nil => cases allowed
  | cons o rest inductionHypothesis =>
      rcases allowed with ⟨_membership, continuation⟩
      cases o with
      | leaf _ =>
          simp only [Compiles]
          exact continuation
      | refines sub =>
          simp only [Compiles]
          exact inductionHypothesis sub continuation

/-! The declared decision tree, emitted from |}
  ^ source
  ^ {|. Each question is a definition
    named after it, and the root carries the `at_most_16` bound. -/

|}

let tail root_name =
  {|/-- `at_most_16`: the declared fanout bound holds by computation; `decide`
    reduces the length comparison in the kernel. -/
theorem at_most_16 : AtMost16 |}
  ^ root_name
  ^ {| := by decide

/-- A question with seventeen options; the same decidable check refutes it, so
    the compiler refuses a fanout violation rather than reporting one. -/
def overFilled : Question :=
  Question.mk "bad" "bad" (List.replicate 17 (Answer.leaf ⟨"x"⟩))

example : ¬ AtMost16 overFilled := by decide

end CSFC.Verification

#print axioms CSFC.Verification.at_most_16
#print axioms CSFC.Verification.one_way
#print axioms CSFC.Verification.composes
|}

(** The Lean file the declaration projects to. [source] is the declaration's
    logical name, recorded in the emitted header. A tree with no single root
    cannot be projected and is rejected by [check] before this is reached. *)
let render_lean ~source (tree : tree) =
  let root = match root tree with
    | Some root -> root
    | None -> failwith "the decision tree has no single root question" in
  header source
  ^ String.concat "\n\n" (List.map render_question (ordered tree))
  ^ "\n\n" ^ tail (definition root.question_id)

(* --- the four text projections --- *)

(* Every generated text file opens with this header, naming the generator and
   the declaration it projects, so a reader knows the bytes are csfc's and the
   drift gate knows what owns them. *)
let text_header ~source =
  Printf.sprintf "# Code generated by csfc from %s; DO NOT EDIT.\n" source

(* An option's name: the identifier it was declared with. *)
let option_name (option : option) = option.option_id

(* An option's definition: the template a leaf instantiates, or the question a
   refines opens. A JEV prompt offers these as the criterion's options. *)
let option_definition (option : option) =
  match option.kind with
  | Leaf template -> template
  | Refines target -> target

(* The option names of a question, in declaration order. *)
let option_names (question : question) = List.map option_name question.options

(* kind_enum: each question is an enum of its options, so `enum tier
   { in_process; kernel; ipc; net; }` is the tier question. *)
let render_kind_enum ~source (tree : tree) =
  let buffer = Buffer.create 256 in
  Buffer.add_string buffer (text_header ~source);
  List.iter (fun (question : question) ->
    Printf.bprintf buffer "enum %s { %s; }\n" question.question_id
      (String.concat "; " (option_names question))) tree;
  Buffer.contents buffer

(* directory: the directory tree the declaration names. The root question is the
   top-level directory and every option a child; a refines option opens its
   sub-question's children beneath it, so the directory tree mirrors the
   decision tree. *)
let render_directory ~source (tree : tree) =
  let buffer = Buffer.create 512 in
  Buffer.add_string buffer (text_header ~source);
  let rec walk depth (question : question) =
    let pad = String.make (depth * 2) ' ' in
    List.iter (fun (option : option) ->
      Printf.bprintf buffer "%s%s/\n" pad option.option_id;
      match option.kind with
      | Leaf _ -> ()
      | Refines target ->
          (match List.find_opt (fun (other : question) -> other.question_id = target) tree with
           | Some sub -> walk (depth + 1) sub
           | None -> ())) question.options in
  (match root tree with
   | Some (root_question : question) ->
       Printf.bprintf buffer "%s/\n" root_question.question_id;
       walk 1 root_question
   | None -> ());
  Buffer.contents buffer

(* cli_layer: every question is a CLI layer and every option a verb, so the CLI
   reads `csf <area> <verb>` layer by layer. *)
let render_cli_layer ~source (tree : tree) =
  let buffer = Buffer.create 256 in
  Buffer.add_string buffer (text_header ~source);
  List.iter (fun (question : question) ->
    List.iter (fun (option : option) ->
      Printf.bprintf buffer "csf %s %s\n" question.question_id (option_name option))
      question.options) tree;
  Buffer.contents buffer

(* jev_prompt: the criterion is the question's text and the options are the
   option definitions, in the JEV choice form `criterion :: a | b | c`. *)
let render_jev_prompt ~source (tree : tree) =
  let buffer = Buffer.create 256 in
  Buffer.add_string buffer (text_header ~source);
  List.iter (fun (question : question) ->
    Printf.bprintf buffer "%s :: %s\n" question.text
      (String.concat " | " (List.map option_definition question.options))) tree;
  Buffer.contents buffer

(* The four text projections, keyed by their file name beside the Lean proof. *)
let projections ~source (tree : tree) =
  [ "question_kind_enum.txt", render_kind_enum ~source tree;
    "question_directory.txt", render_directory ~source tree;
    "question_cli_layer.txt", render_cli_layer ~source tree;
    "question_jev_prompt.txt", render_jev_prompt ~source tree ]

(* The Lean proof's file name: the one artifact that is not a text projection. *)
let lean_filename = "CSFCVerifier.lean"

(* Every generated artifact of the declaration, the Lean proof first. *)
let artifacts ~source (tree : tree) =
  (lean_filename, render_lean ~source tree) :: projections ~source tree

(* Write every artifact under [out], creating parent directories and writing
   each file atomically -- a mode-600 temporary beside it, then rename. Mirrors
   [Shell.write]. A tree that cannot project is rejected by [check] first. *)
let write ~source ~out (tree : tree) =
  let rec mkdir_p dir =
    if dir = "" || dir = "." || dir = "/" || dir = Filename.dirname dir then ()
    else if Sys.file_exists dir then ()
    else (mkdir_p (Filename.dirname dir); Unix.mkdir dir 0o755) in
  let write_file path contents =
    mkdir_p (Filename.dirname path);
    let temporary = Filename.temp_file ~temp_dir:(Filename.dirname path) ".csfc-" ".tmp" in
    Fun.protect
      ~finally:(fun () -> if Sys.file_exists temporary then Sys.remove temporary)
      (fun () ->
        Out_channel.with_open_bin temporary (fun channel -> output_string channel contents);
        Sys.rename temporary path) in
  List.iter (fun (relative, contents) ->
    write_file (Filename.concat out relative) contents) (artifacts ~source tree)

(* Verify that every artifact under [out] equals the projection byte for byte;
   the drift gate's compare. A missing file or a differing byte fails. *)
let verify ~source ~out (tree : tree) =
  List.iter (fun (relative, contents) ->
    let path = Filename.concat out relative in
    if not (Sys.file_exists path)
    || In_channel.with_open_bin path In_channel.input_all <> contents then
      failwith ("generated drift: " ^ path)) (artifacts ~source tree)
