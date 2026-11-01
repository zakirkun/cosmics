---
name: CVSS v4.0 Scoring
category: report
version: 1.0.0
description: Produce a defensible CVSS v4.0 vector and score for a confirmed finding
when_to_use: A finding is confirmed and needs a severity score for the report
stage_affinity: [reporting]
tags: [cvss, severity, scoring, reporting, risk]
requires_tools: []
token_estimate: 950
---

# CVSS v4.0 Scoring

## Preconditions

- The finding is confirmed with evidence. Scoring an unconfirmed hypothesis
  inflates the report; leave it as "possible" confidence with no vector.
- The vulnerable component and the impacted component are identified. They are
  often different systems, and CVSS v4.0 scores that difference explicitly.
- The threat environment is known. If the exploit exists in the wild and is
  reachable by an unauthenticated attacker, the Threat metric reflects that; do
  not assume the worst case by default.

## Procedure

1. Start from the base metrics and choose the value you can defend from evidence.
   `Attack Vector` is the path you actually used, not the path that might exist.
2. Set `Attack Complexity` to High only when a condition outside the attacker's
   control must hold. A payload that requires a specific browser version is High;
   a payload that works whenever the parameter is present is Low.
3. Set `Privileges Required` to the privilege level the attacker holds *before*
   the exploit. Using a low-privileged account you were given still counts as Low.
4. Set `User Interaction` to Active only if a human must take a specific action.
   A page that exploits on load with no click is Passive.
5. Score the CIA impact on the *vulnerable* system first, then the *subsequent*
   system when the exploit pivots. Do not double-count the same impact.
6. Add Threat only when there is a named, observed exploitation source. Otherwise
   leave it at `Not Defined`; the score stays base-only and that is defensible.
7. Record the vector string verbatim next to the score, and keep the reasoning for
   each contested metric in one line. A reviewer must be able to re-derive it.

## Signals

- Your vector scores higher than the vendor's advisory for the same CVE: usually a
  sign that `Privileges Required` or `Attack Complexity` was overstated in your
  favour.
- `Attack Vector: Network` on a vulnerability that needed an authenticated VPN
  session — that is `Adjacent` at best.
- A `Critical` score on a finding with no evidence of impact. Severity without
  demonstrated consequence is the most common reporting defect.

## Common pitfalls

- Copying a vendor's vector without checking it against your own reproduction.
- Mixing CVSS v3.1 and v4.0 metric names in one vector; they are not compatible
  (`Scope` does not exist in v4.0).
- Scoring information disclosure at `High` confidentiality impact for data the
  attacker already had access to.
- Reporting a score without the vector. A bare number cannot be audited.

## Tools and flags

- No tool is required. Compute the score from the v4.0 specification tables and
  record the vector by hand; this keeps the derivation auditable.
- `nuclei` and `sqlmap` emit v3.1 vectors when asked — translate the metrics rather
  than pasting the string.

## ATT&CK / WSTG mapping

- Not an ATT&CK technique. The finding it scores carries the technique mapping;
  this procedure only assigns severity.
