(** Pure, deterministic projections of validated declarations.
    Supply the successful result of [Validate.resolve]; manually fabricated,
    inconsistent resolved graphs are outside this contract. No function reads
    files, writes output, executes tests or observes a running application.
    Equal values, including declaration order and source locations, produce equal
    bytes; these functions do not canonicalize differently ordered declarations. *)

module Label = Emit__Label
module Ocaml = Emit__Ocaml
module Mermaid = Emit__Mermaid
module Diagram = Emit__Diagram
module Review = Emit__Review
module Json = Emit__Json
