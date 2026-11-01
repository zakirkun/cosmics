---
name: Subdomain Enumeration
category: recon
version: 1.0.0
description: Enumerate subdomains from passive sources, then resolve and confirm which ones answer
when_to_use: A domain target is in scope and the engagement needs its full external host surface before port scanning
stage_affinity: [recon, osint]
tags: [dns, subdomain, passive-recon, certificate-transparency]
requires_tools: [amass, dnsx, subfinder, httpx]
token_estimate: 900
---

# Subdomain Enumeration

## Preconditions

- The engagement names a registered domain (`example.com`), not a single host. If
  only a host is in scope, skip enumeration and go straight to service scanning.
- Wildcard DNS may be configured. Confirm it before trusting any result: query
  three random labels (`a1b2c3.example.com`) and check whether they resolve.
- Resolver capacity is available. Public resolvers rate-limit aggressively; use a
  local resolver or a curated list.

## Procedure

1. Collect passive candidates from certificate transparency, then from multiple
   passive DNS aggregators. Never rely on one source: coverage differs sharply by
   region and by how recent the certificate was issued.
2. De-duplicate and lowercase the candidate set. Strip wildcards and anything that
   is not a syntactically valid label sequence under the apex.
3. Resolve every candidate, recording A, AAAA, and CNAME. Follow CNAME chains and
   keep the chain: a subdomain pointing at a third-party SaaS edge is a different
   risk than one pointing at a self-hosted origin.
4. Probe the resolved hosts over HTTP and HTTPS. Record status code, server
   header, title, and TLS certificate subject — the certificate often names
   internal services the DNS record does not.
5. Diff against the previous run. New hosts since the last enumeration are the
   highest-value findings; capture the first-seen timestamp.

## Signals

- A host resolving to RFC1918 space or to a cloud load balancer that answers only
  from inside the VPC — likely an internal service accidentally published in DNS.
- Certificate subjects listing names that DNS enumeration did not return. Feed
  those names back into step 3.
- A wildcard resolution that returns the same address for every label: treat the
  entire namespace as unresolved unless a distinct record exists.

## Common pitfalls

- Reporting wildcard hits as live hosts. This is the single most common source of
  false positives in recon output.
- Forgetting to check AAAA. A host with no A record may still answer over IPv6.
- Trusting a stale passive-DNS entry. Always re-resolve; passive databases retain
  records for months after decommissioning.
- Ignoring CNAME targets that point outside the authorized scope. Enumerate them,
  but do not scan them without written scope extension.

## Tools and flags

- `subfinder -d example.com -all -recursive -o subs.txt` — passive aggregation.
- `amass enum -passive -d example.com -o amass.txt` — second passive source.
- `dnsx -l subs.txt -a -aaaa -cname -resp -o resolved.txt` — bulk resolution.
- `httpx -l resolved.txt -sc -title -server -td -silent` — HTTP probing.
- `dig +short a1b2c3.example.com` — wildcard detection, run three times.

## ATT&CK / WSTG mapping

- WSTG-INFO-01, WSTG-INFO-04 (information gathering, enumeration).
- ATT&CK T1590.001 (Gather Victim Network Information: Domain Properties),
  T1595.001 (Active Scanning: Scanning IP Blocks).
