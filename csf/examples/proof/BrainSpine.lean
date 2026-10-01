import Std.Tactic

set_option autoImplicit false

/-!
The arithmetic slice of go/proto/candace/brainspine/v1/brainspine.proto.
This model has mathematical integers, four input slots, and a partial stack
machine: stack underflow returns none. It makes no physical-safety claim.
-/

namespace BrainSpine

def numericBound : Int := 1000000000

def clamp (lower upper value : Int) : Int :=
  max lower (min upper value)

def saturate (value : Int) : Int :=
  clamp (-numericBound) numericBound value

/-- Int division is Euclidean; splitting on the sign explicitly gives the
profile's division toward zero for the positive denominator 1000. -/
def divideScale (value : Int) : Int :=
  if value < 0 then -((-value) / 1000) else value / 1000

def scaleValue (coefficient value : Int) : Int :=
  saturate (divideScale (value * coefficient))

abbrev Inputs := Fin 4 → Int

inductive Expr where
  | constant (value : Int)
  | input (index : Fin 4)
  | add (left right : Expr)
  | scale (coefficient : Int) (argument : Expr)
  | clamp (lower upper : Int) (argument : Expr)
  deriving Repr

def evaluate (inputs : Inputs) : Expr → Int
  | .constant value => saturate value
  | .input index => saturate (inputs index)
  | .add left right => saturate (evaluate inputs left + evaluate inputs right)
  | .scale coefficient argument => scaleValue coefficient (evaluate inputs argument)
  | .clamp lower upper argument => saturate (clamp lower upper (evaluate inputs argument))

inductive Instruction where
  | constant (value : Int)
  | input (index : Fin 4)
  | add
  | scale (coefficient : Int)
  | clamp (lower upper : Int)
  deriving Repr

abbrev Stack := List Int

def step (inputs : Inputs) (instruction : Instruction) (stack : Stack) : Option Stack :=
  match instruction, stack with
  | .constant value, rest => some (saturate value :: rest)
  | .input index, rest => some (saturate (inputs index) :: rest)
  | .add, right :: left :: rest => some (saturate (left + right) :: rest)
  | .scale coefficient, value :: rest => some (scaleValue coefficient value :: rest)
  | .clamp lower upper, value :: rest => some (saturate (clamp lower upper value) :: rest)
  | _, _ => none

def execute (inputs : Inputs) : List Instruction → Stack → Option Stack
  | [], stack => some stack
  | instruction :: rest, stack => do
      let next ← step inputs instruction stack
      execute inputs rest next

def compile : Expr → List Instruction
  | .constant value => [.constant value]
  | .input index => [.input index]
  | .add left right => compile left ++ compile right ++ [.add]
  | .scale coefficient argument => compile argument ++ [.scale coefficient]
  | .clamp lower upper argument => compile argument ++ [.clamp lower upper]

theorem execute_append (inputs : Inputs) (first second : List Instruction) (stack : Stack) :
    execute inputs (first ++ second) stack =
      (execute inputs first stack).bind (execute inputs second) := by
  induction first generalizing stack with
  | nil => rfl
  | cons instruction rest inductionHypothesis =>
      simp only [List.cons_append, execute]
      cases step inputs instruction stack with
      | none => rfl
      | some next => exact inductionHypothesis next

/-- Every expression compiles to code that pushes precisely its evaluated value
and preserves the entire pre-existing stack. No well-formedness premise is
needed for this theorem about the mathematical AST. -/
theorem compile_correct (inputs : Inputs) (expression : Expr) (stack : Stack) :
    execute inputs (compile expression) stack = some (evaluate inputs expression :: stack) := by
  induction expression generalizing stack with
  | constant value => rfl
  | input index => rfl
  | add left right leftCorrect rightCorrect =>
      simp only [compile, execute_append, leftCorrect, rightCorrect, Option.bind_some]
      rfl
  | scale coefficient argument argumentCorrect =>
      simp only [compile, execute_append, argumentCorrect, Option.bind_some]
      rfl
  | clamp lower upper argument argumentCorrect =>
      simp only [compile, execute_append, argumentCorrect, Option.bind_some]
      rfl

theorem clamp_bounds (lower upper value : Int) (ordered : lower ≤ upper) :
    lower ≤ clamp lower upper value ∧ clamp lower upper value ≤ upper := by
  unfold clamp
  omega

theorem saturate_bounds (value : Int) :
    -numericBound ≤ saturate value ∧ saturate value ≤ numericBound := by
  exact clamp_bounds (-numericBound) numericBound value (by decide)

theorem evaluate_bounds (inputs : Inputs) (expression : Expr) :
    -numericBound ≤ evaluate inputs expression ∧ evaluate inputs expression ≤ numericBound := by
  cases expression <;> simp only [evaluate, scaleValue] <;> exact saturate_bounds _

def actuator (value : Int) : Int := clamp (-1000) 1000 value

theorem actuator_bounds (value : Int) :
    -1000 ≤ actuator value ∧ actuator value ≤ 1000 := by
  exact clamp_bounds (-1000) 1000 value (by decide)

def executeActuator (inputs : Inputs) (code : List Instruction) : Option Int := do
  let stack ← execute inputs code []
  match stack with
  | [value] => some (actuator value)
  | _ => none

/-- The compiled source expression, executed from an empty stack and passed
through the final actuator clamp, has exactly the specified source output. -/
theorem compiled_actuator_correct (inputs : Inputs) (expression : Expr) :
    executeActuator inputs (compile expression) = some (actuator (evaluate inputs expression)) := by
  unfold executeActuator
  rw [compile_correct]
  rfl

theorem compiled_actuator_bounds (inputs : Inputs) (expression : Expr) :
    ∃ output, executeActuator inputs (compile expression) = some output ∧
      -1000 ≤ output ∧ output ≤ 1000 := by
  exact ⟨actuator (evaluate inputs expression), compiled_actuator_correct inputs expression,
    actuator_bounds (evaluate inputs expression)⟩

/- Kernel-checked arithmetic and rejection examples. `decide` produces proof
terms; no native_decide or external prover is used. -/
example : divideScale (-1999) = -1 := by decide
example : divideScale 1999 = 1 := by decide
example : divideScale (-999) = 0 := by decide
example : scaleValue (-1500) 1001 = -1501 := by decide
example : saturate 1000000001 = 1000000000 := by decide
example : saturate (-1000000001) = -1000000000 := by decide
example : execute (fun _ => 0) [.add] [] = none := by decide
example : executeActuator (fun _ => 0) [.constant 1, .constant 2] = none := by decide

end BrainSpine

#print axioms BrainSpine.compile_correct
#print axioms BrainSpine.evaluate_bounds
#print axioms BrainSpine.compiled_actuator_correct
#print axioms BrainSpine.compiled_actuator_bounds
