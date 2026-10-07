#!/usr/bin/env python3
"""
Prose-lint checks for mathematics, contrasts, examples, and evidence.

Rules:
1. math-lint: flag math symbols outside $...$ or $$...$$; flag unbalanced $; flag unknown \\commands in math
2. contrast-lint: flag contrasts without two named sides and example marker in next two sentences
3. example-lint: flag abstract claims without example marker in next two sentences
4. evidence-lint: flag dagger numbers outside References; flag daggers in sections with 'planned'; require banner
"""

import re
import sys
from typing import List, Tuple, Set

# Common LaTeX commands whitelist
LATEX_WHITELIST = {
    # Math modes and text
    r'\mathrm', r'\mathbf', r'\mathit', r'\mathcal', r'\mathbb', r'\mathsf', r'\mathtt',
    # Arrows and relations
    r'\rightarrow', r'\rightharpoonup', r'\leftarrow', r'\leftrightarrow', r'\Rightarrow',
    r'\subseteq', r'\sqsubseteq', r'\supseteq', r'\supset', r'\subset',
    # Quantifiers and operators
    r'\in', r'\notin', r'\forall', r'\exists', r'\wedge', r'\vee', r'\neg',
    r'\implies', r'\iff', r'\lor', r'\land',
    # Comparisons and set operations
    r'\le', r'\ge', r'\ll', r'\gg', r'\neq', r'\approx', r'\equiv',
    r'\cup', r'\cap', r'\setminus', r'\emptyset',
    # Other common commands
    r'\coloneqq', r'\quad', r'\qquad', r'\text', r'\;', r'\,', r'\!', r'\:',
    r'\times', r'\to', r'\mapsto', r'\sum', r'\prod', r'\int', r'\frac',
    r'\ldots', r'\cdots', r'\vdots', r'\ddots',
    r'\bigcup', r'\bigcap', r'\bigsqcup', r'\bigvee', r'\bigwedge',
    r'\pi', r'\sigma', r'\tau', r'\lambda', r'\epsilon', r'\alpha', r'\beta', r'\gamma',
    r'\bigl', r'\bigr', r'\big', r'\Big', r'\Bigl', r'\Bigr',
    r'\ell', r'\infty', r'\partial', r'\nabla', r'\sqrt',
    r'\left', r'\right', r'\textstyle', r'\displaystyle', r'\underline', r'\overline',
    r'\not', r'\texttt', r'\delta', r'\rho', r'\eta', r'\mu', r'\kappa', r'\Lambda', r'\Delta', r'\ln', r'\log', r'\arg', r'\max', r'\min', r'\mid', r'\big|',
}
# This Python check is interim: the DOC lane in tools/house_lint (OCaml) replaces it (#358).

# Math symbols that must be inside $ delimiters
MATH_SYMBOLS = {'∀', '∃', '⟺', '⇒', '⊑', '⊆', '∈', '∉', '∧', '∨', '¬', '≤', '≥', '⋢', '∖', '∪', '∩', '≔'}

# Contrast markers
CONTRAST_MARKERS = {
    'differ', 'differs', 'unlike', 'rather than', 'as opposed to',
    'in contrast', 'whereas', 'instead of'
}

# Abstract claim markers - look for exact phrases or word-boundary versions
CLAIM_MARKERS_EXACT = {
    'the bottleneck',
    'are noisy',
    'cannot be trusted',
    'by construction'
}

CLAIM_MARKERS_WORDS = {
    'needs', 'must', 'should', 'in general'
}

# Example markers
EXAMPLE_MARKERS = {'for example', 'e.g.', 'such as', 'consider', 'concretely'}


def read_file(path: str) -> str:
    """Read file content."""
    with open(path, 'r') as f:
        return f.read()


def extract_lines(text: str) -> List[Tuple[int, str]]:
    """Return list of (line_number, line) tuples."""
    return [(i+1, line) for i, line in enumerate(text.split('\n'))]


def extract_math_regions(text: str) -> List[Tuple[int, int]]:
    """Extract (start, end) positions of $...$ and $$...$$ regions."""
    regions = []
    i = 0
    while i < len(text):
        if i < len(text) - 1 and text[i:i+2] == '$$':
            # Display math $$...$$
            start = i
            i += 2
            end = text.find('$$', i)
            if end == -1:
                end = len(text)
            else:
                end += 2
            regions.append((start, end))
            i = end
        elif text[i] == '$' and (i == 0 or text[i-1] != '\\'):
            # Inline math $...$, but not \$
            start = i
            i += 1
            end = text.find('$', i)
            if end == -1 or text[end-1:end+1] == '\\$':
                i += 1
                continue
            else:
                end += 1
            regions.append((start, end))
            i = end
        else:
            i += 1
    return regions


def is_in_math(pos: int, regions: List[Tuple[int, int]]) -> bool:
    """Check if position is inside a math region."""
    for start, end in regions:
        if start <= pos < end:
            return True
    return False


def get_line_number(pos: int, text: str) -> int:
    """Get line number for a position in text."""
    return text[:pos].count('\n') + 1


def math_lint(text: str) -> List[Tuple[str, int, str, str]]:
    """Check for math symbols outside math mode and other math issues."""
    findings = []

    # Inline code spans are data, not math or prose: blank them (same length) first.
    text = re.sub(r'`[^`\n]*`', lambda m: ' ' * len(m.group(0)), text)
    # Extract math regions
    math_regions = extract_math_regions(text)

    # Check for unbalanced $ delimiters (count of non-escaped $)
    dollar_count = 0
    i = 0
    while i < len(text):
        if i < len(text) - 1 and text[i:i+2] == '$$':
            i += 2
        elif text[i] == '$' and (i == 0 or text[i-1] != '\\'):
            dollar_count += 1
            i += 1
        else:
            i += 1

    if dollar_count % 2 != 0:
        findings.append(('math-lint', 1, 'unbalanced-dollars', 'Unbalanced $ delimiters in file'))

    # Check for math symbols outside math mode
    for i, char in enumerate(text):
        if char in MATH_SYMBOLS and not is_in_math(i, math_regions):
            line_num = get_line_number(i, text)
            start = max(0, i - 15)
            end = min(len(text), i + 15)
            context = text[start:end].replace('\n', ' ')
            findings.append(('math-lint', line_num, f'symbol-outside-math-{char}', f'Symbol {char} outside math: ...{context}...'))

    # Check for unknown LaTeX commands in math
    for start, end in math_regions:
        content = text[start:end]
        for match in re.finditer(r'\\[a-zA-Z]+\{?', content):  # \_ and other escaped punctuation are not commands
            cmd_with_brace = match.group(0)
            # Strip the brace if present for whitelist check
            cmd = cmd_with_brace.rstrip('{').split('_')[0]
            if cmd not in LATEX_WHITELIST:
                pos_in_text = start + match.start()
                line_num = get_line_number(pos_in_text, text)
                # Skip if it looks like a custom subscript like \pi_S
                if not re.match(r'\\[a-z]+_[A-Z]', cmd_with_brace):
                    context = content[max(0, match.start()-10):match.start()+len(cmd)+10].replace('\n', ' ')
                    findings.append(('math-lint', line_num, f'unknown-command-{cmd}', f'Possibly unknown LaTeX: {context}'))

    return findings


def split_sentences(text: str) -> List[Tuple[int, str]]:
    """Split text into sentences. Better handling of abbreviations and bullet points.
    Returns list of (line_number_of_start, sentence_text)."""
    sentences = []

    # Split on periods/question marks/exclamation marks followed by space and capital letter,
    # or followed by newline-dash (bullet points), or followed by newline and capital letter
    pattern = r'(?<=[.!?])\s+(?=[A-Z\-]|$)'

    # But first, let's manually iterate to preserve line numbers
    current_sentence = ""
    current_line_start = 1

    i = 0
    while i < len(text):
        current_sentence += text[i]

        # Check if we've reached end of sentence
        if text[i] in '.!?' and i + 1 < len(text):
            # Look ahead to see if next char is whitespace followed by capital or dash
            j = i + 1
            while j < len(text) and text[j] in ' \t':
                j += 1

            # End sentence if we see capital letter, dash, or newline
            if j < len(text) and (text[j].isupper() or text[j] == '-' or text[j] == '\n'):
                if j < len(text) and text[j] == '\n':
                    current_sentence += '\n'
                    i = j
                else:
                    i = j - 1

                sent_text = current_sentence.strip()
                if sent_text:
                    sentences.append((current_line_start, sent_text))

                current_sentence = ""
                current_line_start = text[:i+1].count('\n') + 1

        # Track newlines for line numbers
        if text[i] == '\n':
            current_line_start = text[:i+1].count('\n') + 1

        i += 1

    # Add remaining
    if current_sentence.strip():
        sentences.append((current_line_start, current_sentence.strip()))

    return sentences


def has_example_marker(text: str) -> bool:
    """Check if text contains an example marker."""
    text_lower = text.lower()
    for marker in EXAMPLE_MARKERS:
        if marker in text_lower:
            return True
    # Also check for code spans
    if '`' in text:
        return True
    return False


def has_claim_marker(text: str) -> bool:
    """Check if text contains an abstract claim marker."""
    text_lower = text.lower()

    # Check exact phrases first
    for marker in CLAIM_MARKERS_EXACT:
        if marker in text_lower:
            return True

    # Check word-boundary matches for single words
    for marker in CLAIM_MARKERS_WORDS:
        if re.search(r'\b' + re.escape(marker) + r'\b', text_lower):
            return True

    return False


def has_contrast_marker(text: str) -> bool:
    """Check if text contains a contrast marker."""
    text_lower = text.lower()
    for marker in CONTRAST_MARKERS:
        if marker in text_lower:
            return True
    return False


def contrast_lint(text: str) -> List[Tuple[str, int, str, str]]:
    """Flag contrasts without two sides and example within next two sentences."""
    findings = []
    sentences = split_sentences(text)

    for sent_idx, (line_num, sentence) in enumerate(sentences):
        if not has_contrast_marker(sentence):
            continue

        # Look for example marker in this and next two sentences
        has_example = False
        for i in range(sent_idx, min(sent_idx + 3, len(sentences))):
            if has_example_marker(sentences[i][1]):
                has_example = True
                break

        # Check for two sides (heuristic: look for comma or "and" suggesting contrast)
        has_two_sides = ',' in sentence or ' and ' in sentence.lower() or ' but ' in sentence.lower()

        if not has_two_sides or not has_example:
            context = sentence[:70].replace('\n', ' ')
            findings.append(('contrast-lint', line_num, 'contrast-no-example', context))

    return findings


def example_lint(text: str) -> List[Tuple[str, int, str, str]]:
    """Flag abstract claims without example marker within next two sentences."""
    findings = []
    sentences = split_sentences(text)

    for sent_idx, (line_num, sentence) in enumerate(sentences):
        if not has_claim_marker(sentence):
            continue

        # Look for example marker in this and next two sentences
        has_example = False
        for i in range(sent_idx, min(sent_idx + 3, len(sentences))):
            if has_example_marker(sentences[i][1]):
                has_example = True
                break

        if not has_example:
            context = sentence[:70].replace('\n', ' ')
            findings.append(('example-lint', line_num, 'claim-no-example', context))

    return findings


def evidence_lint(text: str, lines: List[Tuple[int, str]]) -> List[Tuple[str, int, str, str]]:
    """Check for daggers outside references and daggers in 'planned' sections."""
    findings = []

    # Check for agent-draft/unapproved banner
    if 'agent draft' not in text.lower() or 'unapproved' not in text.lower():
        findings.append(('evidence-lint', 1, 'missing-banner', 'Missing agent-draft/unapproved banner at top'))

    # Find References section line number
    ref_start = None
    for line_num, line in lines:
        if line.lower().startswith('## references') or line.lower().startswith('# references'):
            ref_start = line_num
            break

    # Check for dagger in "planned" sections (lines with *planned*)
    for line_num, line in lines:
        if '*planned*' in line and '†' in line and not line.lstrip().startswith('> **Status'):
            findings.append(('evidence-lint', line_num, 'dagger-in-planned', line[:70].replace('\n', ' ')))

    return findings


def main():
    """Run all checks."""
    if len(sys.argv) < 2:
        print('Usage: prose-lint.py <draft.md>', file=sys.stderr)
        sys.exit(1)

    draft_path = sys.argv[1]
    text = read_file(draft_path)
    lines = extract_lines(text)

    all_findings = []

    # Run all checks
    all_findings.extend(math_lint(text))
    all_findings.extend(contrast_lint(text))
    all_findings.extend(example_lint(text))
    all_findings.extend(evidence_lint(text, lines))

    # Deduplicate and sort
    all_findings = sorted(set(all_findings), key=lambda x: (x[1], x[0]))

    # Print in format: file:line:rule:check_type:excerpt
    if all_findings:
        for rule, line_num, check_type, excerpt in all_findings:
            print(f'{draft_path}:{line_num}:{rule}:{check_type}:{excerpt}')
        sys.exit(1)
    else:
        print(f'{draft_path}: all checks passed')
        sys.exit(0)


if __name__ == '__main__':
    main()
