(** Return the change from [base] to [head] as one Mermaid diagram: the union
    of both declarations, nested as [Emit.Mermaid] nests them, with every
    process, scope, component, dependency and connection marked added (green,
    +), removed (red, dashed, -), changed (amber, ~, with what it was) or
    unchanged (grey). Declared architecture only, like [Emit.Mermaid]. *)
val render : base:Model.resolved -> head:Model.resolved -> string
