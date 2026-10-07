#!/usr/bin/env python3
"""Derive the `dir` declarations for csf/architecture/architecture.csf.

Mirrors the OCaml kind_of_path / ancestors logic in
csf/compiler/architecture/facts.ml. Every tracked directory (every ancestor of
a tracked file, up to and including the root) gets one declaration. A directory
whose files are only reached through subdirectories holds no direct children,
so its allowed set is empty.

Usage:
  census_dirs.py stats   -- print the counts
  census_dirs.py block   -- print the `dir ... ;` declaration block
"""
import os
import subprocess
import sys
import collections

TOOLS = os.path.dirname(os.path.abspath(__file__))


def kind_of(path):
    base = path.rsplit("/", 1)[-1]
    dot = base.rfind(".")
    extension = base[dot + 1:] if (dot >= 0 and dot + 1 < len(base)) else base
    return "".join(
        c.lower() if "A" <= c <= "Z"
        else c if (c.islower() or c.isdigit() or c == "_")
        else "_"
        for c in extension
    )


def parent(path):
    return path.rsplit("/", 1)[0] if "/" in path else "."


def census(root):
    raw = subprocess.check_output(["git", "-C", root, "ls-files", "-z"], text=True)
    files = [f for f in raw.split("\0") if f]
    direct = collections.defaultdict(set)
    for f in files:
        direct[parent(f)].add(kind_of(f))
    every = set()
    for f in files:
        d = parent(f)
        while True:
            every.add(d)
            if d == ".":
                break
            d = d.rsplit("/", 1)[0] if "/" in d else "."
    return files, direct, every


def main():
    mode = sys.argv[1] if len(sys.argv) > 1 else "stats"
    root = subprocess.check_output(
        ["git", "-C", TOOLS, "rev-parse", "--show-toplevel"], text=True).strip()
    files, direct, every = census(root)
    if mode == "stats":
        print("root=%s" % root)
        print("tracked_files=%d" % len(files))
        print("directories_with_files=%d" % len(direct))
        print("tracked_directories=%d" % len(every))
        print("intermediate_only=%d" % len(every - set(direct)))
        return
    lines = []
    for d in sorted(every, key=lambda value: "" if value == "." else value):
        kinds = sorted(direct.get(d, []))
        lines.append('  dir "%s" allowed [%s];' % (d, ", ".join('"%s"' % k for k in kinds)))
    sys.stdout.write("\n".join(lines) + "\n")


if __name__ == "__main__":
    main()
