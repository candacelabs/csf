#!/usr/bin/env python3
"""ghost-terms.py: how many ontology terms own no directory.

Reads csf/compiler/language/architecture.csf at a git revision (default
origin/main) and classifies every `term` as: a directory named after the
term exists; its prose cites a repository path; or neither (no home).
Prints the counts and the no-home list. Exit 0 always; this is a
measurement, not a gate (the gate is source monorepo #386).

Usage: ghost-terms.py [REPO] [REV]
"""
import re, subprocess, sys

repo = sys.argv[1] if len(sys.argv) > 1 else subprocess.run(['git', 'rev-parse', '--show-toplevel'], capture_output=True, text=True).stdout.strip()
rev = sys.argv[2] if len(sys.argv) > 2 else 'origin/main'
src = subprocess.run(['git', 'show', rev + ':csf/compiler/language/architecture.csf'], capture_output=True, text=True, cwd=repo).stdout
terms = re.findall(r'^term\s+(\w+)\s+"([^"]*)"\s+"((?:[^"\\]|\\.)*)"', src, re.M)
tree = [d for d in subprocess.run(['git', 'ls-tree', '-r', '-d', '--name-only', rev], capture_output=True, text=True, cwd=repo).stdout.split('\n') if d]
dirs = {d.split('/')[-1].replace('-', '_') for d in tree}
path = re.compile(r'\b((?:csf|pkg|ipc|io|services|app|tools|runtime|examples|web|proto)/[A-Za-z0-9_./-]+)')
homed, cited, ghost = [], [], []
for name, _title, body in terms:
    if name in dirs:
        homed.append(name)
    elif path.search(body):
        cited.append(name)
    else:
        ghost.append(name)
print('revision=%s terms=%d dir_named_after_term=%d path_cited_in_prose=%d no_home=%d' % (rev, len(terms), len(homed), len(cited), len(ghost)))
print('no_home: ' + ', '.join(ghost))
