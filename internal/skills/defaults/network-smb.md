---
name: SMB Enumeration
category: network
version: 1.0.0
description: Enumerate SMB shares, sessions, and signing posture, then validate anonymous access safely
when_to_use: TCP 445 (or 139) is open on a host in scope and the engagement covers internal network services
stage_affinity: [recon, analysis]
tags: [smb, netbios, shares, null-session, signing, lateral-movement]
requires_tools: [nmap, netexec, smbclient, enum4linux-ng]
token_estimate: 1000
---

# SMB Enumeration

## Preconditions

- Port 445 is reachable and the host is in scope. Confirm with a single TCP
  connect before running any enumerator.
- The engagement permits read-only enumeration. Share listing is passive; opening
  a file inside a share is a data-access action and needs approval where the
  engagement requires it.
- An account is available if anonymous access is disabled. Record which account
  was used for every result so the evidence is attributable.

## Procedure

1. Fingerprint the SMB service: dialect, signing requirement, and OS version.
   Signing being disabled or optional is itself a finding — it enables relay.
2. Attempt a null session. Record whether the server accepts it and what it
   returns. Many modern builds refuse; older builds and appliances often do not.
3. List shares and classify each one: readable, writable, or inaccessible. Do not
   recurse into a readable share without approval; enumerate one level and stop.
4. Enumerate users, groups, and password policy via SAMR and LSA. Password policy
   informs whether credential testing is even viable, so collect it before any
   password work.
5. Run a signed, unauthenticated vulnerability probe limited to checks that do not
   crash the service. SMB enumeration is read-heavy; avoid anything that triggers
   a service restart.
6. Record the signing status, the auth method accepted, and every readable share
   path with its permissions.

## Signals

- `signing:False` or `signing:not_required` in the service output.
- A null session returning a user list — a legacy guest fallback is configured.
- A share named `IPC$` accepting a connection while all disk shares refuse:
  anonymous RPC is reachable even though file access is not.
- Write access on a share from an unauthenticated or low-privilege context.

## Common pitfalls

- Running a full share-recursion over a large file server and stalling the scan.
- Treating a failed null session as "SMB is secure". It only means anonymous RPC
  is refused; authenticated enumeration may still be wide open.
- Missing SMB over NetBIOS (139) when 445 is filtered. Some hosts expose only 139.
- Confusing a permission-denied response with a non-existent share.

## Tools and flags

- `nmap -p 139,445 --script smb2-security-mode,smb2-capabilities,smb-os-discovery <host>`
  — dialect and signing posture without authentication.
- `netexec smb <host> -u '' -p '' --shares` — null-session share listing.
- `netexec smb <host> -u user -p pass --shares --users --pass-pol` — authenticated
  enumeration, one pass.
- `smbclient -L //<host>/ -N` — independent confirmation of the share list.
- `enum4linux-ng -A <host>` — consolidated enumeration when the above disagree.

## ATT&CK / WSTG mapping

- WSTG-CONF-01 (network infrastructure configuration),
  WSTG-ATHZ-02 (authorization bypass through directory traversal of shares).
- ATT&CK T1021.002 (Remote Services: SMB/Windows Admin Shares),
  T1087.002 (Account Discovery: Domain Account).
