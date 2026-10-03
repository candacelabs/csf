# Case 000 — the originating prompt

| | |
|---|---|
| **date** | 2026-09-02 |
| **source** | operator message to the orchestrating session |
| **branch** | `feat/widget-foundry` |
| **program** | Widget Foundry — `docs/widget_foundry.md` |
| **tier** | 2 (judged against `rubric.md`) |

**This file is immutable.** A revised or follow-up prompt becomes a sibling
case (`000a-…`, `000b-…`), never an edit here. The value of the case is that it
is the exact input that produced this skill; edit it and the eval measures a
prompt nobody sent.

The prompt is reproduced verbatim below — typos, run-on sentences, unclosed
parenthesis and all. Do not correct the spelling, expand the abbreviations, or
reflow it. The misspellings (`autegenerated`, `primtivives`, `montior`) are
part of the input distribution the skill has to work in, and a tidied-up prompt
is an easier prompt.

---

```
ok i need a couple new primitives: we need to start building libraries of reusable ui components, like custom animations and shit. think about composable reusable interfaces, interfaces should be named with an I prefix btw, like INewInterface so that it's clear that it's an interface and function params should never be unnamed. like we should create a skill for all my super particular preferences so that agents can write code just as good as me. how do we maximize code written this way with the fewest number of tokens? also autegenerated code is really good and we try to select for that (think about it, sqlc, protobuf custom compiler, etc. like ast bashing is cheap now so we can do it quickly and easily so we have to think about relaxing that constraint when we're reasoning and i think that's letting an agent themselves think about what primtivives they want and devleop a language for it. ontological thinking or whatever i saw that on hackernews or somewhere recently you should look up and figure out what that measn btw but you're the orchestrator, come up with an end goal and montior a team of agnets until it's done and work in vertical slices but don't deploy agents until i sign off on the goal and THEN name the branch and worktree and then this prompt itslef and the creation of this tool itself should be able to be used to improve the tool etc. like all of that has to just be happening in the background without anyone really even noticing and that should result in some empirically demonstrable metrics being captured and that has to be thought out etc. like anyawy figure out what i'm talking about and then come to me with whatever questions you need until you can figure out a good goal i'm happy with
```

---

## What actually happened

Kept here so the rubric can be judged against a real outcome rather than a
hypothetical one: the response asked four decision questions, the operator
answered them, and the answers are recorded as decisions 1–4 of
`docs/widget_foundry.md`. Branch and worktree were named after that sign-off,
not before. This skill, its gate, its census, and this eval corpus are the
substrate slice of the program that came out of it.
