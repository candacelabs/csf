let require condition message = if not condition then failwith message
let contains text piece =
  let length = String.length piece in
  let rec loop n = n + length <= String.length text &&
    (String.sub text n length = piece || loop (n + 1)) in
  loop 0
let () =
  let grammar = In_channel.with_open_bin Sys.argv.(1) In_channel.input_all in
  let output = Highlight_codegen.generate ~grammar in
  require (output = Highlight_codegen.generate ~grammar) "generation must be deterministic";
  if Array.length Sys.argv <> 5 then failwith "expected grammar and three generated files";
  List.iteri (fun index (_, expected) ->
    let path = Sys.argv.(index + 2) in
    require (In_channel.with_open_bin path In_channel.input_all = expected)
      ("generated highlighting drift: " ^ path)) output;
  let json = Yojson.Safe.from_string (List.assoc "src/grammar.json" output) in
  let open Yojson.Safe.Util in
  let rules = json |> member "rules" |> to_assoc in
  require (fst (List.hd rules) = "architecture") "start rule must remain first";
  let fixture = {|root = "newkeyword", identifier, ["!"], {item}, ""; item = "child" | integer;|} in
  let projected = Highlight_codegen.generate ~grammar:fixture in
  require (contains (List.assoc "queries/highlights.scm" projected) "\"newkeyword\"") "query must derive new keywords";
  require (contains (List.assoc "src/scanner.c" projected) "\"newkeyword\"") "scanner must reserve new keywords";
  require (contains (List.assoc "src/grammar.json" projected) "REPEAT") "EBNF repetition must project";
  require (contains (List.assoc "src/grammar.json" projected) "BLANK") "EBNF option and empty terminal must project";
  List.iter (fun invalid ->
    match Highlight_codegen.generate ~grammar:invalid with
    | _ -> failwith "invalid grammar was accepted"
    | exception Frontend_lexer.Error _ -> ())
    ["root = missing;"; "root = { [\"x\"] };"; "root = comment; comment = \"x\";"];
  print_endline "highlight generator tests passed"
