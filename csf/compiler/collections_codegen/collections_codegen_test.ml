(* golden_first step 4: the generator's output is the hand-written golden, byte
   for byte. The golden path arrives as argv(1) through the Bazel data edge; a
   plain run falls back to the repository path. A mismatch prints the first
   differing byte and fails, so the emitted source and the pinned golden move
   together. *)

let read path =
  let channel = open_in_bin path in
  Fun.protect ~finally:(fun () -> close_in channel)
    (fun () -> really_input_string channel (in_channel_length channel))

let first_difference generated golden =
  let limit = min (String.length generated) (String.length golden) in
  let rec scan index =
    if index >= limit then index
    else if generated.[index] = golden.[index] then scan (index + 1)
    else index in
  scan 0

let () =
  let golden_path =
    if Array.length Sys.argv > 1 then Sys.argv.(1)
    else "pkg/collections/testdata/golden/collections.go" in
  let golden = read golden_path in
  let generated = Collections_codegen.render () in
  if generated <> golden then begin
    let at = first_difference generated golden in
    let shows value = if at < String.length value then String.make 1 value.[at] else "<end of file>" in
    Printf.eprintf "collections_codegen: %s: generated %d bytes, golden %d bytes; first difference at byte %d\n"
      golden_path (String.length generated) (String.length golden) at;
    Printf.eprintf "collections_codegen: generated has %s, golden has %s\n" (shows generated) (shows golden);
    exit 1
  end;
  Printf.printf "collections_codegen: %s matches the generated output byte for byte (%d bytes)\n"
    golden_path (String.length golden)
