#!/bin/bash
# cite-check.sh: verify citations against Crossref and arXiv APIs

PASS=0
FAIL=0

# Temp file for results
RESULTS=$(mktemp)

# Trap to clean up temp file
cleanup() {
    rm -f "$RESULTS"
}
trap cleanup EXIT

# Parse and verify citations
verify_citation() {
    local key="$1"
    local title="$2"
    local author="$3"
    local year="$4"
    local url="$5"
    local doi="$6"
    local arxiv="$7"

    local verdict="UNKNOWN"
    local resolved_title=""
    local resolved_author=""
    local resolved_year=""
    local resolved_url="$url"

    # If we have a DOI, query Crossref
    if [ -n "$doi" ]; then
        local response
        response=$(curl -s "https://api.crossref.org/works/$doi" 2>/dev/null || echo "")
        
        if [ -n "$response" ] && echo "$response" | jq -e '.message' >/dev/null 2>&1; then
            resolved_title=$(echo "$response" | jq -r '.message.title[0] // empty' 2>/dev/null || echo "")
            resolved_author=$(echo "$response" | jq -r '.message.author[0].family // empty' 2>/dev/null || echo "")
            resolved_year=$(echo "$response" | jq -r '.message.issued."date-parts"[0][0] // empty' 2>/dev/null || echo "")
            
            if [ -n "$resolved_title" ]; then
                verdict="PASS"
            else
                verdict="FAIL"
            fi
        else
            verdict="FAIL"
        fi
    fi

    # If we have an arXiv ID, query arXiv
    if [ "$verdict" = "UNKNOWN" ] && [ -n "$arxiv" ]; then
        local response
        response=$(curl -s "https://export.arxiv.org/api/query?id_list=$arxiv" 2>/dev/null || echo "")
        
        if [ -n "$response" ] && echo "$response" | grep -q "entry"; then
            resolved_title=$(echo "$response" | grep -oP '(?<=<title>)[^<]+' | head -1 | sed 's/^ *//;s/ *$//')
            resolved_author=$(echo "$response" | grep -oP '(?<=<name>)[^<]+' | head -1)
            resolved_year=$(echo "$response" | grep -oP '(?<=published>)[0-9]+' | head -1)
            resolved_url="https://arxiv.org/abs/$arxiv"
            verdict="PASS"
        else
            verdict="FAIL"
        fi
    fi

    # Check URL if verdict is still UNKNOWN and we have a URL
    if [ "$verdict" = "UNKNOWN" ] && [ -n "$url" ]; then
        local http_code
        http_code=$(curl -s -I "$url" --max-time 5 2>/dev/null | head -1 | grep -oE '[0-9]{3}' || echo "0")
        if [ "$http_code" = "200" ] || [ "$http_code" = "301" ] || [ "$http_code" = "302" ]; then
            verdict="PASS"
        else
            verdict="FAIL"
        fi
    fi

    # Default to FAIL if still UNKNOWN
    if [ "$verdict" = "UNKNOWN" ]; then
        verdict="FAIL"
    fi

    # Count results
    if [ "$verdict" = "PASS" ]; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
    fi

    # Write result
    printf "%s|%s|%s|%s|%s|%s\n" "$key" "$resolved_title" "$resolved_author" "$resolved_year" "$resolved_url" "$verdict" >> "$RESULTS"
}

# Extract and verify each reference
verify_citation "[1]" "The ANSI/X3/SPARC DBMS framework report" "Tsichritzis" "1978" "https://doi.org/10.1016/0306-4379(78)90001-7" "10.1016/0306-4379(78)90001-7" ""

verify_citation "[2]" "A relational model of data for large shared data banks" "Codd" "1970" "https://doi.org/10.1145/362384.362685" "10.1145/362384.362685" ""

verify_citation "[3]" "Aspect-oriented programming" "Kiczales" "1997" "https://doi.org/10.1007/BFb0053381" "10.1007/BFb0053381" ""

verify_citation "[4]" "What you always wanted to know about Datalog" "Ceri" "1989" "https://doi.org/10.1109/69.43410" "10.1109/69.43410" ""

verify_citation "[5]" "Reflexion: language agents with verbal reinforcement learning" "Shinn" "2023" "" "" "2303.11366"

verify_citation "[6]" "DSPy: compiling declarative language model calls" "Khattab" "2023" "" "" "2310.03714"

verify_citation "[7]" "Modeling by shortest data description" "Rissanen" "1978" "" "10.1016/0005-1098(78)90005-5" ""

# Print header and results
echo ""
echo "Citation Verification Results"
echo "============================="
echo ""
printf "%-6s | %-48s | %-12s | %-4s | %-50s | %-6s\n" "Key" "Title" "Author" "Year" "URL" "Verdict"
printf "%-6s | %-48s | %-12s | %-4s | %-50s | %-6s\n" "------" "------------------------------------------------" "------------" "----" "--------------------------------------------------" "------"

# Read and display results
if [ -f "$RESULTS" ]; then
    while IFS='|' read -r key title author year url verdict; do
        title_short="${title:0:48}"
        author_short="${author:0:12}"
        url_short="${url:0:50}"
        printf "%-6s | %-48s | %-12s | %-4s | %-50s | %-6s\n" "$key" "$title_short" "$author_short" "$year" "$url_short" "$verdict"
    done < "$RESULTS"
fi

echo ""
echo "Summary: $PASS passed, $FAIL failed"
echo ""

# Exit code based on pass/fail
if [ "$FAIL" -eq 0 ]; then
    exit 0
else
    exit 1
fi
