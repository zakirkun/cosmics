---
name: Injection Testing
category: web
version: 1.0.0
description: Detect and confirm SQL, command, template, and NoSQL injection with non-destructive payloads
when_to_use: An endpoint accepts user-controlled input that reaches a database, shell, template engine, or query language
stage_affinity: [analysis, exploit]
tags: [sqli, command-injection, ssti, nosql, payloads, waf]
requires_tools: [sqlmap, ffuf, nuclei, curl]
token_estimate: 1300
---

# Injection Testing

## Preconditions

- Inputs are mapped: query parameters, body fields, headers, cookies, path
  segments, and any value that is later reflected into a file path or a template.
- You can observe a differential response — either a boolean oracle, a time delay,
  or an error. Without an oracle, no confirmation is possible and the finding must
  stay at "possible" confidence.
- Destructive payloads are out of scope unless the engagement explicitly permits
  them. Default to read-only confirmation (`AND 1=1`, `SLEEP(0)`, `SELECT 1`).

## Procedure

1. Baseline the endpoint three times with the parameter removed, then with a
   benign value. Record status, length, body hash, and latency. All later
   comparisons use this baseline.
2. Test boolean-based SQL injection with paired predicates that must differ:
   `1' AND '1'='1` versus `1' AND '1'='2`. A stable difference in body or length
   across repeated trials is the signal; noise is not.
3. Test time-based injection where the boolean oracle is filtered. Use a
   deterministic delay (five seconds) and verify the delay scales with a doubled
   value. A single slow response is a network artifact, not a finding.
4. For command injection, prefer a DNS or HTTP callback over in-band output. A
   blind callback is unambiguous and does not depend on how the output is parsed.
5. For template injection, escalate through arithmetic rather than string
   literals: `{{7*7}}` returning `49` is proof of evaluation, while a reflected
   `{{7*7}}` string is not. Continue with the engine-specific object graph only
   far enough to prove execution.
6. For NoSQL injection, try operator injection in JSON bodies
   (`{"password":{"$ne":null}}`) and in query strings (`password[$ne]=x`). Many
   stacks accept one form and not the other.
7. Once confirmed, reduce to the smallest payload that still demonstrates the
   behaviour. Save the exact request, the exact response, and the timestamp.

## Signals

- Response length differs consistently for true versus false predicates across at
  least three pairs.
- A latency jump that scales with the requested delay value.
- A DNS or HTTP callback arriving from the target's egress address.
- Verbose database errors leaking a dialect-specific string (`ORA-`, `pg_`,
  `SQLSTATE`) — proof the input reached a parser, not yet proof of injection.

## Common pitfalls

- Declaring SQL injection from a single 500 response. That is a parser error, not
  an exploit; a WAF often produces the same response.
- Using `sqlmap`'s aggressive modes against a production system without approval.
- Missing injection inside JSON values because the body was only ever tested as a
  form-encoded request.
- Reporting a reflected payload as injection. Reflection is not execution; prove
  evaluation with an arithmetic or callback oracle.

## Tools and flags

- `sqlmap -r request.txt -p id --batch --level 3 --risk 1 --technique=BT` — safe
  techniques only: boolean and time.
- `ffuf -u "https://app.example.com/item?id=FUZZ" -w payloads.txt -mc all -fs 512`
  — differential fuzzing against a known response size.
- `curl -s -o /dev/null -w '%{time_total}\n' "https://app.example.com/item?id=1'%20AND%20SLEEP(5)--"`
  — one-shot timing probe.
- `nuclei -u https://app.example.com -t http/vulnerabilities/ -tags sqli,ssti` —
  known-pattern coverage.

## ATT&CK / WSTG mapping

- WSTG-INPV-05 (SQL Injection), WSTG-INPV-11 (Code Injection),
  WSTG-INPV-18 (Server-Side Template Injection).
- ATT&CK T1190 (Exploit Public-Facing Application), T1059 (Command and Scripting
  Interpreter).
