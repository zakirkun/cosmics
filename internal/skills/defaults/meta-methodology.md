---
name: Methodology Overview
category: meta
version: 1.0.0
description: The engagement operating loop that ties recon, analysis, exploitation, and reporting into one auditable sequence
when_to_use: Starting a new engagement, resuming after a pause, or deciding what the next stage should do
stage_affinity: [recon, osint, analysis, mapper, exploit, reporting]
tags: [methodology, process, scope, evidence, decision-making]
requires_tools: []
token_estimate: 1000
---

# Methodology Overview

## Preconditions

- Written authorization is on file and names every target. Without it, no action
  is taken, including passive DNS lookups against the client's name.
- The engagement mode is known. `recon` stops at enumeration, `assess` adds
  analysis and attack mapping, `exploit` adds proof-of-concept validation, and
  `full` adds post-exploitation.
- The budget is visible. Iterations, tokens, and currency are capped, and running
  out mid-stage leaves an engagement in a state someone must reconcile.

## Procedure

1. Convert scope into an explicit target list with kinds (domain, host, IP, CIDR,
   URL, repo, binary). Anything not on that list is out of scope even if it is
   discovered later.
2. Recon first and completely. Every later stage consumes the asset graph, so a
   missing host is a finding the engagement will never make. Record assets with
   their source so a disputed entry can be traced.
3. OSINT second, against the target organisation rather than the infrastructure:
   certificate transparency, leaked credentials, public repositories, and
   technology disclosures. Keep it passive; do not touch the client's hosts.
4. Analysis consolidates observations into facts. A fact is a durable statement
   with a subject, a namespaced predicate, a value, a confidence, and a source.
   When a new observation contradicts an old one with equal or higher confidence,
   the old fact is superseded, not deleted.
5. The mapper builds the attack graph from facts: what is reachable, what exploits
   what, what yields access. Each edge carries a cost and a confidence; the graph
   is a planning artefact, not a claim of compromise.
6. Exploitation validates the highest-value edges with the least intrusive proof
   that is unambiguous. Every action is recorded with its exact command, image,
   and output hash. Destructive actions require explicit human approval.
7. Reporting renders from the database, not from memory. Findings need evidence
   rows; an unconfirmed finding stays unconfirmed and is labelled as such.

## Signals

- A stage that "completes" with no facts produced: usually the exit criterion was
  misread, or the agent stopped early. Re-check the criterion before advancing.
- The same vulnerability reappearing across many hosts. Group it as one finding
  with a list of affected assets rather than N near-identical findings.
- Facts that keep flip-flopping between values. That is a signal the collection
  method is unreliable, not that the target changed.

## Common pitfalls

- Treating a discovered host as authorization to scan it. Discovered ≠ in scope.
- Advancing stages to keep momentum. Each stage exit predicate is a gate, and
  skipping one produces a report nobody can defend.
- Storing observations in notes instead of facts. Notes are not retrieved into
  context and are not auditable.
- Running an exploit before the mapper has recorded why. The report then cannot
  explain the impact chain.

## Tools and flags

- No tools at this level. This procedure selects *which* stage runs; each stage
  has its own skill with concrete tooling.
- Stage transitions are recorded as events; check the latest ones before deciding
  the next action, so a resumed engagement does not repeat completed work.

## ATT&CK / WSTG mapping

- Frames the whole of WSTG; each stage maps to one WSTG category group
  (Information Gathering, Configuration, Authentication, Authorization, etc.).
- ATT&CK Enterprise tactics are mapped per finding, never per engagement.
