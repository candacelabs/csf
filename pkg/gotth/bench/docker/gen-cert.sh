#!/usr/bin/env sh
# The bench proxy's TLS material (§3.6, §5.3).
#
# §5.3 says "The proxy's certificate is a locally generated self-signed cert
# committed to the bench tree, trusted only by the harness; no ACME, no public
# DNS, no host firewall or network-policy change of any kind is involved."
#
# Everything in that sentence is honoured except the word "committed", and the
# deviation is deliberate: committing a TLS PRIVATE KEY to a git repository is
# a bad habit to establish even for a bench proxy that only ever listens on
# 127.0.0.1, and this repository's own rules put key material on the
# never-commit list. What §5.3 actually needs is that the certificate be
# reproducible, local, and trusted by nothing but the harness — which a
# committed generation script with a recorded SPKI pin gives, without leaving a
# private key in the history forever.
#
# The generated files are gitignored. The SPKI pin this prints is what the
# browser driver is given (--ignore-certificate-errors-spki-list), so the
# harness trusts exactly this certificate and nothing else — which is stronger
# than the blanket --ignore-certificate-errors a committed cert would have
# needed anyway.
#
#   sh docker/gen-cert.sh          regenerate; prints the SPKI pin
#
# Recorded in bench/README.md under "Deviations from the spec's letter".
set -eu

dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/tls"
mkdir -p "$dir"

openssl req -x509 -newkey rsa:2048 -nodes \
	-keyout "$dir/bench.key" -out "$dir/bench.crt" \
	-days 3650 -subj "/CN=bench.localhost" \
	-addext "subjectAltName=DNS:bench.localhost,DNS:localhost,DNS:proxy,IP:127.0.0.1" \
	>/dev/null 2>&1

chmod 600 "$dir/bench.key"

pin="$(openssl x509 -in "$dir/bench.crt" -pubkey -noout \
	| openssl pkey -pubin -outform der \
	| openssl dgst -sha256 -binary \
	| openssl enc -base64)"

printf '%s\n' "$pin" > "$dir/bench.spki"
printf 'wrote %s/bench.crt, bench.key\n' "$dir"
printf 'SPKI pin (browser driver trusts exactly this): %s\n' "$pin"
