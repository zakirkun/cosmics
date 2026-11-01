---
name: Web Authentication Testing
category: web
version: 1.0.0
description: Test login, session, MFA, and password-reset flows for authentication weaknesses
when_to_use: The target exposes a login form, a session cookie, an API token endpoint, or a password reset flow
stage_affinity: [analysis, exploit]
tags: [auth, session, jwt, oauth, brute-force, mfa, password-reset]
requires_tools: [ffuf, hydra, nuclei, jwt-tool]
token_estimate: 1200
---

# Web Authentication Testing

## Preconditions

- An account you are authorized to use exists. Create two accounts where the
  engagement allows it: cross-account tests need a second identity.
- Rate limits are known or can be measured. Establishing the lockout threshold is
  itself a finding; crossing it accidentally can deny service to real users.
- Credential handling is settled before any test: captured credentials are
  written to the encrypted store, never to logs, notes, or the report body.

## Procedure

1. Map every authentication entry point: form login, API token issuance, SSO
   callback, mobile login endpoint, password reset, invite acceptance, and any
   legacy endpoint still routed.
2. Test username enumeration through response differences — message text, HTTP
   status, response timing, and reset-flow behavior. Confirm timing findings with
   repeated measurement; a single sample proves nothing.
3. Test password policy and lockout. Increment failed attempts on one account and
   record the response at each step. Note whether the lockout is per-account, per
   IP, or absent.
4. Exercise the reset flow end to end with a mailbox you control. Check whether
   the token is bound to the account, whether it expires, whether it is reusable,
   and whether the response leaks the account's existence.
5. Inspect how session state is held: cookie attributes (`HttpOnly`, `Secure`,
   `SameSite`, `Path`, `Domain`), token storage in the browser, and whether the
   session identifier changes at login. Session fixation remains common.
6. For token-based auth, decode the token without trusting it. Verify the
   signature algorithm is pinned server-side by presenting a token with the
   algorithm set to `none`, then a token signed with the public key as an HMAC
   secret. A server that accepts either is critically broken.
7. Test MFA: whether it can be skipped by calling the post-login endpoint
   directly, whether the "remember this device" cookie is guessable, and whether
   recovery codes are rate limited.

## Signals

- Identical response body with a different HTTP status for valid versus invalid
  usernames — enumeration through status codes only.
- A session cookie whose value is unchanged after a successful login.
- A reset token that survives its own use, or one whose entropy looks like a
  counter or a timestamp.
- A `JWT` whose header advertises `alg: HS256` while the server also publishes an
  RS256 public key.

## Common pitfalls

- Locking out the account under test and then reporting "no other findings" when
  the remaining tests never ran.
- Testing logout by deleting the cookie client-side instead of invalidating the
  server-side session.
- Assuming an SSO flow is safe because the identity provider is reputable. The
  vulnerable seam is almost always the application's callback handler.
- Sending real user passwords to a brute-force tool. Only use accounts created for
  the engagement.

## Tools and flags

- `ffuf -u https://app.example.com/FUZZ -w users.txt -mc all -fs 1234` — username
  enumeration by response size.
- `hydra -l user@example.com -P passwords.txt app.example.com https-post-form
  "/login:username=^USER^&password=^PASS^:F=Invalid"` — rate-limited credential
  testing against an authorized account only.
- `nuclei -u https://app.example.com -t http/misconfiguration/ -tags auth` —
  known authentication misconfigurations.
- `jwt_tool <token> -M at -t https://app.example.com/api/me` — algorithm
  confusion checks.

## ATT&CK / WSTG mapping

- WSTG-ATHN-01 through WSTG-ATHN-07 (authentication testing).
- ATT&CK T1110 (Brute Force), T1539 (Steal Web Session Cookie),
  T1556 (Modify Authentication Process).
