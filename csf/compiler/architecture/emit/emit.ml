(* The Emit namespace. Each projection is its own file under emit/, compiled
   as Emit__<Name> (dune's convention) and exposed here as Emit.<Name>. *)
module Label = Emit__Label
module Ocaml = Emit__Ocaml
module Mermaid = Emit__Mermaid
module Diagram = Emit__Diagram
module Review = Emit__Review
module Json = Emit__Json
