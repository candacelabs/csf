# Golden: the email service

`email` is the first of two fixtures under `golden/service`, declared from the
same `service` kind and checked by the same `service.rules.dl`. This one is a
reverse-port: `services/email` already exists, hand-written, and `email.csf` is
the smaller source a generator should have been able to read it from. The tree
under `services/email/` here is written by hand, not generated; it is the exact
Go the generator should emit, so an operator can read and reject it before a
line of generator is written.

## The declaration

`email.csf` declares one [service](../../../../../docs/generated/ontology_cgen.md#term-service):
the `send` operation with three bounded outcomes (`accepted`, `failed`,
`unknown`), three [capabilities](../../../../../docs/generated/ontology_cgen.md#term-capability)
— `transport`, `provenance`, `receipts` — a three-field `config`, and no stores
or rest calls. Every boundary is a capability the host configures; `Send` is the
one operation. Its body is three `logic` rules and its outcomes are `when`
clauses (`accepted when delivery = accepted`, `failed when delivery = failed`,
`unknown otherwise`).

## Retained regions

`email` is reverse-ported, so its tree carries retained behaviour the
declaration must account for. Three regions are `declared`: the operation body
(`op_body`), the outcome classifier (`classify`), and the config validation
(`validate_config`). The four the declaration cannot yet express as rules are
asked in `email.csf` as typed `question`s, each with one `option` naming the
kind it needs (the decision vocabulary #520 declares in the meta language):

- `parse_addresses` — which primitive parses and bounds one RFC 5322 address and
  rejects a header injection (`address_validation`).
- `render_message` — which primitive serializes one bounded message into a
  complete RFC 5322 message (`message_renderer`).
- `receipt_evidence` — which primitive assembles pre-send and final receipt
  evidence without inventing facts (`receipt_assembly`).
- `normalize_outcome` — which rule maps a transport's invalid outcome to the
  bounded unknown outcome (`outcome`).

`grep -n '^question ' email/email.csf` lists exactly these four. There is no
hand-written seam in the tree.

## What is generated

Everything under `services/email/` is generated from `email.csf`, laid out by
declared thing: `email.go` (the [service](../../../../../docs/generated/ontology_cgen.md#term-service), its config, its options and its
constructor), `send.go` (the operation, its records, its evidence sequence and
its metrics), `transport.go`, `provenance.go` and `receipts.go` (one file per
capability, each with its option), a test file beside each declared thing
(`send_test.go` holds the operation's outcome specs; `transport_test.go`,
`provenance_test.go` and `receipts_test.go` each hold the capability's gomock
double and its misuse spec), then `email_suite_test.go` and `doc.go`.

## Method and scope

The brief's `golden_first` runs in five steps. This slice delivers steps 1
(declare) and 2 (handwrite), then the held review that opens step 3. Steps 4
(generate) and 5 (replace) do not start until the operator approves this golden.
