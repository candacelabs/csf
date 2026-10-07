#!/usr/bin/env python3
"""bib.py: generate the References section from canonical citation keys.

A citation in the paper is a key, never text. Keys are lower-cased DOIs or
arxiv:<id> (version stripped). Entries are rendered from the registry of
record: Crossref, then DataCite, for DOIs; the arXiv API for arXiv ids.
Exit 1 if any key cannot be resolved or two keys canonicalize to one work.

Usage: bib.py keys.txt            # prints Markdown reference entries
       bib.py --check keys.txt draft.md   # also fails on a [key] in the
                                         # draft that is not in keys.txt,
                                         # or a key never cited
"""
import json, re, subprocess, sys, urllib.parse

def fetch(url, accept=None):
    cmd = ['curl', '-s', '-m', '20', '-L', url]
    if accept:
        cmd[1:1] = ['-H', 'Accept: ' + accept]
    return subprocess.run(cmd, capture_output=True, text=True).stdout

def canon(key):
    key = key.strip()
    m = re.search(r'10\.\d{4,9}/\S+', key)
    if m:
        return 'doi:' + m.group(0).rstrip('.);').lower()
    m = re.search(r'(\d{4}\.\d{4,5})', key)
    if m:
        return 'arxiv:' + m.group(1)
    # A standards document with no DOI: the key is its canonical URL, and the
    # entry is rendered from the page's own <title>; the year and body must be
    # given after '|' because the registry of record is the page itself.
    if key.startswith('https://'):
        return 'url:' + key.split()[0].rstrip('/')
    raise SystemExit('bib: not a DOI, arXiv id or https URL: ' + key)

def webpage(url):
    html = fetch(url)
    m = re.search(r'<title>(.*?)</title>', html, re.S | re.I)
    if not m:
        return None
    title = re.sub(r'\s+', ' ', m.group(1)).strip()
    year = re.search(r'\b(19|20)\d{2}\b', url)
    return dict(title=title, authors='', year=year.group(0) if year else '', where=url.split('/')[2], url=url)

def crossref(doi):
    j = fetch('https://api.crossref.org/works/' + urllib.parse.quote(doi, safe='/'))
    try:
        m = json.loads(j)['message']
    except ValueError:
        return None
    authors = ', '.join(('%s %s' % (a.get('given', ''), a.get('family', ''))).strip() for a in m.get('author', []))
    year = m.get('issued', {}).get('date-parts', [[None]])[0][0]
    venue = (m.get('container-title') or [''])[0]
    vol = m.get('volume', ''); issue = m.get('issue', ''); page = m.get('page', '')
    where = venue
    if vol:
        where += ' ' + vol + (('(' + issue + ')') if issue else '')
    if page:
        where += ':' + page
    return dict(title=m['title'][0], authors=authors, year=year, where=where, url='https://doi.org/' + doi)

def datacite(doi):
    j = fetch('https://api.datacite.org/dois/' + urllib.parse.quote(doi, safe='/'))
    try:
        a = json.loads(j)['data']['attributes']
    except (ValueError, KeyError):
        return None
    authors = ', '.join(c.get('name', '') for c in a.get('creators', []))
    where = (a.get('container') or {}).get('title', '') or a.get('publisher', '')
    return dict(title=a['titles'][0]['title'], authors=authors, year=a.get('publicationYear'), where=where, url='https://doi.org/' + doi)

def arxiv(aid):
    x = fetch('https://export.arxiv.org/api/query?id_list=' + aid)
    entry = re.search(r'<entry>(.*?)</entry>', x, re.S)
    if not entry:
        return None
    e = entry.group(1)
    title = re.sub(r'\s+', ' ', re.search(r'<title>(.*?)</title>', e, re.S).group(1)).strip()
    authors = ', '.join(re.findall(r'<name>(.*?)</name>', e))
    year = int(re.search(r'<published>(\d{4})', e).group(1))
    return dict(title=title, authors=authors, year=year, where='arXiv:' + aid, url='https://arxiv.org/abs/' + aid)

def resolve(key):
    kind, ident = key.split(':', 1)
    if kind == 'doi':
        return crossref(ident) or datacite(ident)
    if kind == 'url':
        return webpage(ident)
    return arxiv(ident)

def main(argv):
    check = argv[0] == '--check'
    if check:
        argv = argv[1:]
    keys_path = argv[0]
    labels, seen, failures = [], {}, []
    for line in open(keys_path):
        line = line.split('#')[0].strip()
        if not line:
            continue
        label, raw = line.split(None, 1)
        c = canon(raw)
        if c in seen:
            failures.append('duplicate: %s and %s are both %s' % (seen[c], label, c))
        seen[c] = label
        labels.append((label, c))
    entries = []
    for label, c in labels:
        r = resolve(c)
        if not r:
            failures.append('unresolved: %s (%s)' % (label, c))
            continue
        entries.append((label, r))
        print('- <a id="ref-%s"></a>**[%s]** %s. *%s.* %s, %s. <%s>' % (label, label, r['authors'], r['title'], r['where'], r['year'], r['url']))
    if check:
        draft = open(argv[1]).read()
        cited = set(re.findall(r'\[([a-z0-9-]+)\]', draft)) - {'verify'}
        known = {l for l, _ in labels}
        for k in sorted(cited - known):
            if not k.isdigit():
                failures.append('cited but not a key: [%s]' % k)
        for k in sorted(known - cited):
            failures.append('key never cited: [%s]' % k)
    for f in failures:
        print('bib: ' + f, file=sys.stderr)
    return 1 if failures else 0

if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
