/-
SessionContainment: no process or container started by session s outlives
cancel(s).

Model: processes P, cgroups C, sessions S. A session has a root process placed
in its cgroup at start; every other process it starts is a fork of one it
already started (setsid, nohup and double fork are forks). Containers are
processes dockerd forks, outside the session's fork tree; the session only asks
for them, with a label. cancel(s) writes 1 to cgroup(s)/cgroup.kill, then
removes every container labelled s.

Lemmas are hypotheses, each with its source:
  L1  kernel cgroup v2 docs, "Processes": a forked child is born into its
      parent's cgroup; only a write to cgroup.procs migrates.
      https://docs.kernel.org/admin-guide/cgroup-v2.html
  L2  same document, "cgroup.kill": every process in the cgroup tree is
      SIGKILLed.
  L3  docker container ls, --filter label: containers are reachable by label.
      https://docs.docker.com/reference/cli/docker/container/ls/
-/

namespace SessionContainment

/-- spawned*(s, p): the session's transitive fork tree from its root. -/
inductive Spawned {P : Type} (root : P) (forks : P → P → Prop) : P → Prop
  | root : Spawned root forks root
  | fork {p q : P} : Spawned root forks p → forks p q → Spawned root forks q

/-- I, process half: everything in the fork tree is in cgroup(s). -/
theorem I_procs {P C : Type} (cgs : C) (root : P) (cg : P → C)
    (forks : P → P → Prop)
    (root_placed : cg root = cgs)
    (L1 : ∀ p q, forks p q → cg q = cg p) :
    ∀ p, Spawned root forks p → cg p = cgs := by
  intro p h
  induction h with
  | root => exact root_placed
  | fork _ hf ih => rw [L1 _ _ hf]; exact ih

/-- G: everything session s started is dead after cancel(s). -/
theorem G {P C S : Type} (s : S) (cgs : C) (root : P) (cg : P → C)
    (forks : P → P → Prop) (container : P → Prop) (requested : S → P → Prop)
    (label : P → Option S) (dead : P → Prop)
    (root_placed : cg root = cgs)
    (L1 : ∀ p q, forks p q → cg q = cg p)
    (L2 : ∀ p, cg p = cgs → dead p)
    (L3 : ∀ p, container p → label p = some s → dead p)
    (labelled : ∀ p, requested s p → label p = some s) :
    ∀ p, (Spawned root forks p ∨ (container p ∧ requested s p)) → dead p := by
  intro p h
  cases h with
  | inl hs => exact L2 p (I_procs cgs root cg forks root_placed L1 p hs)
  | inr hcr => exact L3 p hcr.1 (labelled p hcr.2)

end SessionContainment

#print axioms SessionContainment.I_procs
#print axioms SessionContainment.G
