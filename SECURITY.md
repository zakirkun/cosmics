# Security Policy

## Reporting a vulnerability in Cosmics

Cosmics is an offensive security tool, so a defect in it can cause harm to
systems that were never in scope. Please report privately rather than opening a
public issue.

Use GitHub's private vulnerability reporting on this repository
(Security → Report a vulnerability), or email the maintainer listed in the
commit history.

Include what you can:

- the affected version or commit,
- a description of the defect and its impact,
- the smallest reproduction you have,
- whether the issue requires an authorized engagement to trigger.

Please do not include real credentials, customer data, or third-party target
information in a report. A redacted reproduction is more useful than a complete
one that leaks someone else's data.

## What this project will never do

These are design commitments, not aspirational statements. A report that one of
them has been broken is a security report:

- **Scope is not technically enforced** (decision D1). Cosmics records the
  authorization an operator asserts and audits what was done. It cannot verify
  that you were permitted. Do not expect it to stop you; expect it to leave
  evidence of what you did.
- **No offensive action runs outside a container.** Host tooling is not used.
- **A destructive action requires a persisted approval row.** Denial or timeout
  must leave the run recorded as rejected with no container started.
- **A finding is verified only by evidence**, never by a model's claim: a
  proof-of-concept row plus a producing run that exited zero.
- **Credentials are encrypted at rest and never enter LLM context.** Reports
  render a reference, never a secret.
- **The audit trail is append-only and hash-chained.** An update or delete is
  rejected by a database trigger, not by convention.

## Supported versions

The project is pre-1.0. Security fixes land on `master`; there are no maintained
release branches yet. When tagged releases exist, this section will name them.

## Operator responsibilities

You are responsible for having written authorization for every target you
configure. The absence of a technical gate is deliberate and is documented in
`docs/PRD.md` (decision D1). Using Cosmics against systems you do not own or have
permission to test is illegal in most jurisdictions.
