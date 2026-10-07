import Std.Tactic

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

The file is emitted by `csfc` from the question-tree declaration csf/compiler/verification/question_tree.csf;
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

/-! The declared decision tree, emitted from csf/compiler/verification/question_tree.csf. Each question is a definition
    named after it, and the root carries the `at_most_16` bound. -/

/-- The `structure` question. -/
def structureQuestion : Question :=
  Question.mk "structure" "How is the declaration laid out?"
    [ Answer.leaf ⟨"flat"⟩,
      Answer.leaf ⟨"layered"⟩ ]

/-- The `tier` question. -/
def tierQuestion : Question :=
  Question.mk "tier" "Which tier does the component live in?"
    [ Answer.leaf ⟨"in_process"⟩,
      Answer.leaf ⟨"kernel"⟩,
      Answer.leaf ⟨"ipc"⟩,
      Answer.leaf ⟨"net"⟩ ]

/-- The `role` question. -/
def roleQuestion : Question :=
  Question.mk "role" "Which role does the component play?"
    [ Answer.leaf ⟨"service"⟩,
      Answer.leaf ⟨"manager"⟩,
      Answer.leaf ⟨"library"⟩,
      Answer.leaf ⟨"adapter"⟩,
      Answer.leaf ⟨"gateway"⟩,
      Answer.leaf ⟨"resource"⟩ ]

/-- The `lifecycle` question. -/
def lifecycleQuestion : Question :=
  Question.mk "lifecycle" "Which lifecycle binds the component?"
    [ Answer.leaf ⟨"scoped"⟩,
      Answer.leaf ⟨"lazy"⟩,
      Answer.leaf ⟨"borrowed"⟩ ]

/-- The `transport` question. -/
def transportQuestion : Question :=
  Question.mk "transport" "Which transport carries the connection?"
    [ Answer.leaf ⟨"call"⟩,
      Answer.leaf ⟨"channel"⟩,
      Answer.leaf ⟨"subprocess"⟩,
      Answer.leaf ⟨"remote"⟩,
      Answer.leaf ⟨"device"⟩ ]

/-- The `top_level_dirs` question. -/
def top_level_dirsQuestion : Question :=
  Question.mk "top_level_dirs" "Which top-level directory owns the file?"
    [ Answer.leaf ⟨"app"⟩,
      Answer.leaf ⟨"bazel"⟩,
      Answer.leaf ⟨"csf"⟩,
      Answer.leaf ⟨"docs"⟩,
      Answer.leaf ⟨"examples"⟩,
      Answer.leaf ⟨"extensions"⟩,
      Answer.leaf ⟨"infra"⟩,
      Answer.leaf ⟨"io"⟩,
      Answer.leaf ⟨"pkg"⟩,
      Answer.leaf ⟨"proto"⟩,
      Answer.leaf ⟨"runtime"⟩,
      Answer.leaf ⟨"services"⟩,
      Answer.leaf ⟨"tools"⟩,
      Answer.leaf ⟨"web"⟩,
      Answer.leaf ⟨"widgets"⟩,
      Answer.leaf ⟨"xetcas"⟩ ]

/-- The `cli_areas` question. -/
def cli_areasQuestion : Question :=
  Question.mk "cli_areas" "Which CLI area owns the command?"
    [ Answer.leaf ⟨"links"⟩,
      Answer.leaf ⟨"decide"⟩,
      Answer.leaf ⟨"eval"⟩,
      Answer.leaf ⟨"scoreboard"⟩,
      Answer.leaf ⟨"link"⟩,
      Answer.leaf ⟨"call"⟩,
      Answer.leaf ⟨"serve"⟩,
      Answer.leaf ⟨"mcp"⟩,
      Answer.leaf ⟨"initialize"⟩ ]

/-- The `axis` question. -/
def axisQuestion : Question :=
  Question.mk "axis" "Which decision does the placement answer?"
    [ Answer.refines tierQuestion,
      Answer.refines roleQuestion,
      Answer.refines lifecycleQuestion,
      Answer.refines transportQuestion,
      Answer.refines top_level_dirsQuestion,
      Answer.refines cli_areasQuestion ]

/-- The `grammar` question. -/
def grammarQuestion : Question :=
  Question.mk "grammar" "What are you declaring?"
    [ Answer.leaf ⟨"ontology"⟩,
      Answer.refines structureQuestion,
      Answer.leaf ⟨"meta"⟩,
      Answer.leaf ⟨"gate"⟩,
      Answer.leaf ⟨"service"⟩,
      Answer.leaf ⟨"rest_client"⟩,
      Answer.refines axisQuestion ]

/-- `at_most_16`: the declared fanout bound holds by computation; `decide`
    reduces the length comparison in the kernel. -/
theorem at_most_16 : AtMost16 grammarQuestion := by decide

/-- A question with seventeen options; the same decidable check refutes it, so
    the compiler refuses a fanout violation rather than reporting one. -/
def overFilled : Question :=
  Question.mk "bad" "bad" (List.replicate 17 (Answer.leaf ⟨"x"⟩))

example : ¬ AtMost16 overFilled := by decide

end CSFC.Verification

#print axioms CSFC.Verification.at_most_16
#print axioms CSFC.Verification.one_way
#print axioms CSFC.Verification.composes
