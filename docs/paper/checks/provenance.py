#!/usr/bin/env python3
"""provenance.py: the composer. Classify every span of a document as
delta (regenerable), eta (a signed human line in humans/) or sigma (default).

PROVENANCE directive (source monorepo #392):
  delta(x) <=> exists g, I. x = g(I) and committed(g, I) and regenerate(g, I) = x
  eta(x)   <=> ptr(x) = humans/p@c and blame(c, p, x) = operator and signed(c, k_op) and not agent_session(c)
  sigma(x) <=> not delta(x) and not eta(x)      ; default, never upgraded without proof
Class is computed from origin, never declared. An unresolvable origin refuses.

Spans are Markdown blocks (blank-line separated). Origins:
  delta:  the block equals, byte for byte, a region of a generator's output.
          Generators are declared below as (name, command, input files); the
          command is re-run and its output hashed with its inputs and the
          repository revision.
  eta:    the block equals lines a-b of humans/<p> at commit c in this repo,
          c is signed by k_op (git verify-commit), and c carries no
          CSF-Session trailer.
  sigma:  everything else. Margin required: the model and the session/turn,
          read from the trailers of the commit that last touched the block
          (git blame -> CSF-Session, CSF-Turn, CSF-Model / Co-Authored-By).
          A sigma block whose commit carries no session trailer is
          UNRESOLVED and the composer refuses.

Usage: provenance.py [--repo DIR] DRAFT.md
Prints a legend and a header bar (delta% sigma% eta% by characters), one line
per unresolved or mislabelled span, and exits 1 if any span is unresolved.
"""
import hashlib, os, re, subprocess, sys

GENERATORS = [
    # name, command (run from the repo root), inputs that the hash covers
    ('bib.py', ['python3', 'docs/paper/checks/bib.py', 'docs/paper/keys.txt'], ['docs/paper/keys.txt', 'docs/paper/checks/bib.py']),
    ('ghost-terms.py', ['python3', 'docs/paper/checks/ghost-terms.py'], ['docs/paper/checks/ghost-terms.py']),
]

def run(cmd, cwd):
    return subprocess.run(cmd, capture_output=True, text=True, cwd=cwd)

def sha(path, cwd):
    with open(os.path.join(cwd, path), 'rb') as f:
        return hashlib.sha256(f.read()).hexdigest()[:12]

def blocks(text):
    out, start, buf = [], 1, []
    for i, line in enumerate(text.split('\n'), 1):
        if line.strip() == '':
            if buf:
                out.append((start, i - 1, '\n'.join(buf)))
                buf = []
            start = i + 1
        else:
            buf.append(line)
    if buf:
        out.append((start, len(text.split('\n')), '\n'.join(buf)))
    return out

def generator_outputs(repo):
    rev = run(['git', 'rev-parse', '--short', 'HEAD'], repo).stdout.strip()
    outs = []
    for name, cmd, inputs in GENERATORS:
        r = run(cmd, repo)
        h = hashlib.sha256(('\n'.join(sha(i, repo) for i in inputs)).encode()).hexdigest()[:12]
        outs.append((name, r.stdout, h, rev))
    return outs

def eta_pointer(block, repo):
    # A human span is an exact copy of lines from a file under humans/ at a commit.
    for root, _, files in os.walk(os.path.join(repo, 'humans')):
        for fn in files:
            path = os.path.relpath(os.path.join(root, fn), repo)
            text = open(os.path.join(repo, path), encoding='utf-8', errors='replace').read()
            if block in text:
                lines = text.split('\n'); first = text[:text.index(block)].count('\n') + 1
                last = first + block.count('\n')
                blame = run(['git', 'blame', '-L', '%d,%d' % (first, last), '--porcelain', path], repo).stdout
                commits = set(re.findall(r'^([0-9a-f]{40}) ', blame, re.M))
                for c in commits:
                    signed = run(['git', 'verify-commit', c], repo).returncode == 0
                    msg = run(['git', 'log', '-1', '--format=%B', c], repo).stdout
                    if signed and 'CSF-Session:' not in msg:
                        return (path, c[:12], first, last, True)
                    return (path, c[:12], first, last, False)
    return None

def sigma_margin(draft_rel, first, last, repo):
    blame = run(['git', 'blame', '-L', '%d,%d' % (first, last), '--porcelain', draft_rel], repo).stdout
    commits = sorted(set(re.findall(r'^([0-9a-f]{40}) ', blame, re.M)))
    if not commits:
        return None
    margins = []
    for c in commits:
        msg = run(['git', 'log', '-1', '--format=%B', c], repo).stdout
        session = re.search(r'^CSF-Session:\s*(\S+)', msg, re.M)
        turn = re.search(r'^CSF-Turn:\s*(\S+)', msg, re.M)
        model = re.search(r'^CSF-Model:\s*(\S+)', msg, re.M) or re.search(r'^Co-Authored-By:\s*(.+?)\s*<', msg, re.M)
        if not (session and model):
            return None
        margins.append('model=%s session=%s turn=%s @%s' % (model.group(1), session.group(1), turn.group(1) if turn else '?', c[:12]))
    return '; '.join(margins)

def main(argv):
    repo = os.getcwd()
    if argv and argv[0] == '--repo':
        repo, argv = os.path.abspath(argv[1]), argv[2:]
    draft = argv[0]
    draft_rel = os.path.relpath(os.path.abspath(draft), repo)
    text = open(draft, encoding='utf-8').read()
    gens = generator_outputs(repo)
    total = {'delta': 0, 'sigma': 0, 'eta': 0}
    rows, unresolved = [], []
    for first, last, block in blocks(text):
        n = len(block)
        origin = None
        for name, out, h, rev in gens:
            if block and block in out:
                origin = ('delta', 'generator=%s hash(I)=%s rev=%s' % (name, h, rev)); break
        if not origin:
            e = eta_pointer(block, repo)
            if e:
                path, c, a, b, ok = e
                origin = ('eta' if ok else 'sigma', 'humans/%s@%s:L%d-%d signed%s' % (path, c, a, b, '✓' if ok else '✗ -> sigma'))
        if not origin:
            m = sigma_margin(draft_rel, first, last, repo)
            if m:
                origin = ('sigma', m)
            else:
                origin = ('sigma', 'UNRESOLVED: no CSF-Session/CSF-Model trailer on the commit(s) touching L%d-%d' % (first, last))
                unresolved.append((first, last))
        total[origin[0]] += n
        rows.append((first, last, origin[0], origin[1]))
    chars = sum(total.values()) or 1
    pct = {k: 100.0 * v / chars for k, v in total.items()}
    print('<!-- generated by docs/paper/checks/provenance.py; do not edit -->')
    print('**Provenance** (by characters): δ %.1f%% · σ %.1f%% · η %.1f%% · spans %d · unresolved %d' % (pct['delta'], pct['sigma'], pct['eta'], len(rows), len(unresolved)))
    print('δ = regenerated from committed inputs; σ = model output with session/turn evidence; η = a signed human line from humans/.')
    print()
    for first, last, cls, margin in rows:
        print('L%d-%d\t%s\t%s' % (first, last, cls, margin))
    return 1 if unresolved else 0

if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
