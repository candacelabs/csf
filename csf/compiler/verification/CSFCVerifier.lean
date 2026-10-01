/- CSFC compiler verification interface. This is a stub, not a proof. -/
namespace CSFC.Verification

/-- No successful verification result is available until compiler semantics
and the corresponding correctness theorem are implemented. -/
inductive Result where
  | notImplemented
  deriving Repr, DecidableEq

/-- Source and target types remain caller-owned until their formal semantics
are specified. This entrypoint performs no verification and issues no certificate. -/
def verify {Source Target : Type} (_source : Source) (_target : Target) : Result :=
  .notImplemented

end CSFC.Verification
