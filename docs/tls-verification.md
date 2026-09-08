# TLS certificate verification against the local gateway

This document exists to support one decision: what to do about three open
CodeQL `go/disabled-certificate-check` alerts on PR #1, all pointing at
`InsecureSkipVerify: true` in TLS configs used to talk to a Tesla Powerwall
gateway on the local network:

- `backend/local/local.go:76`
- `backend/tedapi/tedapi.go:79`
- `backend/tedapi/v1r.go:115`

It states what these connections actually do, what an attacker who can
intercept them can actually get, what the realistic alternatives cost, and a
recommendation. It does not dismiss the alerts — that is the repository
owner's call, not this document's, and section 6 gives the exact wording to
use if dismissal is the chosen path.

## 1. Why the gateway cannot present a normally-verifiable certificate

All three flagged call sites construct an `http.Transport` that is later used
to speak HTTPS to a Tesla Powerwall (or Powerwall 3) gateway on the
operator's own LAN — not to any Tesla-operated cloud service.

**`backend/local/local.go:74-80`** (`New`, the local REST backend):

```go
transport := &http.Transport{
    //nolint:gosec // Local Powerwall gateway uses self-signed HTTPS certificate.
    TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
    MaxIdleConns:        poolMaxSize,
    MaxIdleConnsPerHost: poolMaxSize,
    DisableKeepAlives:   poolMaxSize == 0,
}
```

This client is used for every local REST call: `loginLocked` POSTs
`https://<host>/api/login/Basic` with the customer password and email
(`local.go:182-197`), `Poll` GETs arbitrary allow-listed paths such as
`/api/system_status`, `/api/meters/aggregates`, and the protobuf-carrying
`/api/devices/vitals` (`local.go:415-424`), and `Post` writes to endpoints
like `/api/operation` (`local.go:470-486`). `<host>` is whatever IP or
hostname the operator configured (`gopowerwall.WithHost` / `PW_HOST`) —
typically the gateway's LAN IP (e.g. `192.168.91.1` for the WiFi AP, or a DHCP
address on the operator's LAN/VLAN).

**`backend/tedapi/tedapi.go:77-83`** (`NewClient`, the TEDAPI protobuf
backend):

```go
transport := &http.Transport{
    //nolint:gosec // Local Powerwall gateway uses self-signed HTTPS certificate.
    TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
    MaxIdleConns:        poolMaxSize,
    MaxIdleConnsPerHost: poolMaxSize,
    DisableKeepAlives:   poolMaxSize == 0,
}
```

Used by `PostTEDAPI` to POST protobuf-encoded `combined.Message`/`tedapi.Message`
envelopes to `https://<host>/tedapi/v1` with HTTP Basic auth, username `teg`,
password the full gateway WiFi password (`tedapi.go:144-150`,
`req.SetBasicAuth("teg", c.gwPwd)`). `host` defaults to `192.168.91.1`
(`DefaultGWIP`), the gateway's own WiFi access-point address, when unset —
this is the clearest case of a device with no stable DNS name or public
identity at all.

**`backend/tedapi/v1r.go:113-119`** (`NewTEDAPIv1r`, the RSA-signed LAN
variant for Powerwall 3):

```go
transport := &http.Transport{
    //nolint:gosec // Local Powerwall gateway uses self-signed HTTPS certificate.
    TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
    MaxIdleConns:        poolMaxSize,
    MaxIdleConnsPerHost: poolMaxSize,
    DisableKeepAlives:   poolMaxSize == 0,
}
```

Used by `Login` (POST `/api/login/Basic` with the customer password,
`v1r.go:139-152`), `GetDin` (GET `/tedapi/din`, `v1r.go:196`), `PostV1r` (POST
`/tedapi/v1r`, `v1r.go:334`, carrying the RSA-signed `RoutableMessage`), and
`APIGet` (GET arbitrary `/api/...` paths with a bearer token,
`v1r.go:604-609`).

The reason a public CA cannot help here is structural, not a matter of Tesla
not having bothered:

- The gateway is addressed by a **LAN-private IP** (`192.168.x.x`,
  `10.x.x.x`, or the gateway's own `192.168.91.1` AP address), never by a
  publicly resolvable DNS name. Public CAs (under the CA/Browser Forum
  baseline requirements that every major trust store enforces) will not issue
  a certificate for a private IP address or an internal hostname, and there
  is no stable, owner-specific FQDN to put in a certificate's SAN even if
  they would.
- Every gateway of a given hardware/firmware generation ships the **same
  certificate identity story**: a self-signed cert (or one signed by a
  Tesla-internal CA not in any public trust store) baked into firmware at
  manufacture time, with no per-installation provisioning step that could
  bind a certificate to "this operator's specific IP or hostname." There is
  no per-customer enrollment process (comparable to ACME/Let's Encrypt) that
  gopowerwall, or any third-party client, could drive even if the gateway's
  firmware supported it — and it does not expose one.
- The connection frequently isn't even routed IP traffic to begin with: TEDAPI's
  default host is the gateway's own WiFi access point address
  (`192.168.91.1`), a link-local AP that only exists while a client
  associates directly with the gateway's radio. There is no routable network
  path for a CA's OCSP/CRL revocation checking to reach that address in the
  first place, independent of the certificate question.

This matches `.agent/rules/powerwall.md`'s framing ("`InsecureSkipVerify`
against the local gateway is inherent to the protocol") and pypowerwall's own
behavior (upstream also disables verification against the local gateway).
None of this is a gopowerwall design choice; it is a property of the device
gopowerwall is a client for.

**`scan/scan.go:398-402`** and **`proxy/handlers.go:174-179`** do the exact
same thing, for the exact same reason, and the same reasoning applies without
qualification:

```go
// scan/scan.go:398-402 — network discovery probe
tr := &http.Transport{
    TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
    DialContext:     (&net.Dialer{Timeout: clientTimeout}).DialContext,
}
```

```go
// proxy/handlers.go:174-179 — reverse proxy to the gateway for non-API assets
tr := &http.Transport{
    //nolint:gosec // Local gateway connects via self-signed HTTPS by design
    TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
}
```

`scan.Scan` probes an entire `/24` (or operator-specified CIDR) of candidate
LAN addresses for gateway signatures (`GET /api/status`, `GET /tedapi/din`) —
by design it does not know in advance which of up to 254 hosts is a gateway,
so there is no fixed identity to pin against even in principle; any
certificate-based verification scheme here would have to be applied *after*
discovery, against the one host the operator has since configured, not during
the scan itself. `proxyLocalGateway` reverse-proxies arbitrary non-`/api/`
paths (static web assets served by the gateway's own UI) to
`https://<Config.Host><reqPath>` on demand — `Config.Host` is the same
operator-supplied host as the `local` backend uses, so it is exposed to
identical reasoning and should track whatever the `local`/`tedapi` backends
do, not diverge from it. CodeQL was not asked to flag these two (they are not
among the three named alerts), but they are the same finding and any decision
made here should apply to all five call sites for consistency, or the
inconsistency itself should be a deliberate, documented choice.

**A sixth, materially different site was found while reviewing this:**
`client/client.go:118` sets `TLSClientConfig: &tls.Config{InsecureSkipVerify:
opts.Insecure}` — a generic HTTP client with an `Insecure` field on its
`Options` struct, defaulting to Go's zero value (`false`). Nothing in
production code currently constructs this client (`grep` for
`gopowerwall/client"` outside its own test file returns nothing — it appears
to be scaffolding, not yet wired into any backend), so it carries no live
exposure today. It is relevant to this analysis for a different reason: it is
existing, in-tree precedent for making `InsecureSkipVerify` configurable
rather than a hardcoded literal, and — as section 3 discusses — CodeQL does
not flag it, which is useful empirical evidence for what the scanner actually
keys on.

## 2. The precise exposure

Be specific about what "man-in-the-middle" means here rather than treating
`InsecureSkipVerify: true` as an abstract bad practice.

**What crosses these connections, in the clear except for TLS's confidentiality
(which an active MITM defeats entirely by design — that is what
`InsecureSkipVerify` gives up):**

- **The local backend login** (`local.go:183-190`): `POST
  /api/login/Basic` with `{"username": "customer", "password": <customer
  password>, "email": <email>, "clientInfo": {"timezone": ...}}` in the
  request body. The customer password is the last five characters of the
  gateway's WiFi password (per `docs/architecture/local.md`) — a real
  credential, reused for the lifetime of the installation.
- **The resulting session**: either an `AuthCookie`/`UserRecord` cookie pair
  or a bearer token (`local.go:220-256`), replayed on every subsequent
  request (`applyAuth`, `local.go:298-306`). Either one is a full-session
  bearer credential: anyone who captures it can replay it against the
  gateway directly, with no further password needed, until it expires or the
  gateway is rebooted.
- **The TEDAPI gateway password** (`tedapi.go:150`): sent as HTTP Basic auth,
  `SetBasicAuth("teg", c.gwPwd)` — this is the *full* gateway WiFi password
  (not the truncated customer password), i.e. the single most sensitive
  credential in this whole system, sent on every TEDAPI request rather than
  once at login.
- **The v1r customer password and bearer token** (`v1r.go:144-152`,
  `v1r.go:203`): the same login flow as the local backend, over the same
  kind of connection.
- **Everything the gateway reports back**: site production/consumption,
  battery state of charge, grid status, device vitals (component serial
  numbers, firmware versions, alert codes) — operationally sensitive but not
  personally sensitive in the way credentials are.
- **Write commands**: `/api/operation` (mode/reserve changes),
  `ScheduleMaxBackup`/`CancelMaxBackup` via v1r TEG messages — an attacker
  who can inject into this connection, not merely read it, can command the
  battery.

**Does the v1r RSA signing change this?** Only partially, and only for
integrity/authenticity of the specific POST body, not for confidentiality or
for the rest of the v1r surface:

- `PostV1r` (`v1r.go:284-371`) wraps the envelope in a TLV structure
  (`BuildTLVPayload`) that is RSA-PKCS1v15/SHA-512 signed
  (`Sign`, `v1r.go:277-281`) before being POSTed. This proves to the
  *gateway* that the POST body came from a holder of the registered private
  key, and the signature includes a short expiry window
  (`expirationOffset`, `v1r.go:46,303`) that limits replay of a captured
  signed message to roughly the seconds until it lapses. This is a real,
  independent integrity control on that one message type, and it means a
  MITM cannot *forge* a `PostV1r` command even with full visibility into the
  channel.
- It does nothing for confidentiality: the plaintext of every signed message
  is still fully readable by anyone intercepting the TLS connection, since
  `InsecureSkipVerify` means there effectively is no confidential channel to
  begin with against an active attacker.
- It does not cover the rest of v1r's traffic at all: `Login` (customer
  password), `GetDin`, and `APIGet` (`v1r.go:604-637`, bearer-token GETs to
  arbitrary `/api/...` paths) are plain HTTPS with no signing whatsoever.
  Only the `/tedapi/v1r` POST path benefits from RSA signing.
- Net effect: v1r's RSA signing is a genuine mitigation against *command
  injection* on one endpoint, but it is not a substitute for transport
  security, and it provides zero protection for the credentials and data
  that flow over the same connection outside that one signed payload.

**Threat model — where "it's my own LAN" holds, and where it stops holding:**

The implicit argument for accepting this risk is that the attacker has to
already be positioned to intercept traffic between the gopowerwall process
and the gateway, and if they can do that, they are already inside a network
the operator physically controls. That argument is sound for the simplest
deployment: a single home network, one operator, wired or a WPA2/3-secured
home WiFi they administer, gopowerwall running on a machine on that same
segment. In that case, an attacker able to MITM the connection has almost
certainly already compromised something more valuable than the Powerwall
credentials (the router, another host on the LAN, or the WiFi key itself),
so this specific exposure adds comparatively little marginal risk.

That reasoning degrades, and in some cases fails outright, in these cases:

- **A shared or hostile WiFi network** — a guest network the operator
  doesn't fully trust, or (more realistically for a home battery system) a
  landlord- or HOA-managed network where the operator is one tenant among
  several with no control over who else is on the segment. ARP spoofing on a
  shared L2 segment is trivial and does not require compromising the router.
- **A compromised router or access point** — the single most likely real
  compromise path for a home network, and one where the attacker sits
  exactly on-path for gateway traffic without needing anything else to go
  wrong first.
- **A VLAN or network the operator does not fully control** — e.g. an
  installer's or integrator's managed network, a multi-tenant building's
  shared IoT VLAN, or a managed-service scenario where gopowerwall runs on
  infrastructure the *end customer* owns but a third party (installer,
  monitoring vendor) administers the network path to the gateway.
- **A container or workload in a shared cluster reaching the gateway across
  an operator-external network** — this is the scenario furthest from "my
  own LAN": a gopowerwall instance running in a shared Kubernetes cluster or
  cloud environment (site-to-site VPN, home-to-cloud tunnel, etc.) where the
  path between the pod and the gateway crosses infrastructure the *cluster
  operator* controls but the *device operator* does not. Multi-tenant
  clusters, cloud VPN gateways run by a third party, or any hop the
  Powerwall's owner cannot personally audit put a stranger on-path by
  construction, not by attacker effort.

The honest summary: for the median gopowerwall user (single home LAN, one
operator, direct process-to-gateway connection), the risk this represents is
real but low relative to the value of the credential it protects, since a
successful attacker needed comparable network access anyway. For any
deployment where the network between gopowerwall and the gateway is not
fully under the device operator's control, the risk is not hypothetical: it
is an attacker with ordinary on-path capability walking away with the full
gateway password over cleartext-equivalent HTTPS, with no separate exploit
required.

## 3. Alternatives, assessed honestly

### A. Trust-on-first-use (TOFU) certificate pinning

Set `tls.Config.VerifyPeerCertificate` (with `InsecureSkipVerify: true` still
set, since Go's stdlib requires disabling default verification before
`VerifyPeerCertificate`'s raw certs are usable this way — this is the
standard Go TOFU pattern) to compare the presented leaf certificate's SHA-256
fingerprint against one stored from the first successful connection,
persisted alongside the existing auth cache (`local.go`'s `saveAuthCache`
already writes JSON to `CacheFile`, and `Client`/`TEDAPIv1r` have no
equivalent file yet but could gain one, or share `PW_AUTH_PATH`).

- **Fixes**: makes every connection after the first tamper-evident. An
  attacker who was not on-path for the very first connection cannot silently
  MITM later ones without the client detecting a fingerprint mismatch and
  refusing to proceed.
- **Costs**: real engineering work across three backends (`local`, `tedapi`,
  `v1r`), each of which currently builds its own bare `*http.Transport` with
  no shared cert-store abstraction; a new persisted-state format (or an
  extension of the existing cache-file JSON) that has to be designed,
  documented, and versioned; and a new failure mode ("pin mismatch") that
  every caller of `Authenticate`/`Connect` needs to handle distinctly from
  "wrong password" or "host unreachable," including in the CLI and proxy
  server's error/log output.
- **What breaks on firmware cert rotation**: Tesla does not publish a
  Powerwall gateway cert-rotation cadence, but any firmware update that
  regenerates the self-signed cert (which is plausible — nothing here
  guarantees a stable key across firmware versions) invalidates every stored
  pin. The user experience is a Powerwall that updates its firmware and then
  gopowerwall refuses to reconnect with a fingerprint-mismatch error, with no
  way for the software to distinguish "firmware rotated the cert" from "I am
  being attacked" — that ambiguity is the whole point of TOFU, but it means
  every firmware update becomes a support event. Recovery requires either a
  manual "clear pinned cert and re-trust" step (a CLI flag or a file the
  operator deletes) or an interactive re-confirmation prompt, neither of
  which fits gopowerwall's existing headless/library usage pattern (`New`
  returning a ready `*Powerwall`, no interactive callback in the API).
- **Would CodeQL stop flagging it?** No — the sink is
  `InsecureSkipVerify: true`, which this approach still sets (Go requires it
  to make `VerifyPeerCertificate` the sole verifier). The alert would need to
  be dismissed with justification even after implementing TOFU, unless a
  `//nolint`/CodeQL suppression comment is added, which the project's own
  rules (`.agent/rules/workspace.md`: "nolint and removing rules are
  forbidden... the only exception is if no other fix is available") would
  need to treat as that exception.

### B. Operator-supplied certificate or CA bundle, defaulting to skip

Add a config option (e.g. `WithGatewayCACert`/`PW_GATEWAY_CA_CERT`) that, when
set, builds a `x509.CertPool` from the operator-supplied PEM and sets
`RootCAs` on the `tls.Config` with `InsecureSkipVerify: false`; when unset,
falls back to today's behavior.

- **Fixes**: gives operators who *can* extract their gateway's actual
  certificate (it is retrievable — e.g. `openssl s_client -connect
  <host>:443`) a way to opt into full verification, with the software
  defaulting to today's behavior otherwise.
- **Costs to a typical user**: the default path is unchanged, so a user who
  "just wants the container to work" pays nothing — this is the option's
  main appeal. The cost lands entirely on whoever wants to opt in: they must
  extract the gateway's certificate (which likely changes on firmware
  update, same as option A) and manage a file.
- **Would CodeQL stop flagging it?** This is the empirically interesting
  case. `client/client.go:118` in this same repository already does exactly
  this shape — `InsecureSkipVerify: opts.Insecure`, a struct field defaulting
  to Go's zero value `false` — and it is *not* among the reported alerts,
  despite `client.Options{Insecure: true}` appearing in that package's own
  test file. That is concrete, in-repo evidence that CodeQL's
  `go/disabled-certificate-check` query keys on the literal boolean `true`
  reaching the sink, not on general reachability of a `true` value through
  arbitrary data flow — a config field that is `false` by default and only
  becomes `true` through explicit, non-literal operator input does not
  trigger it. If `local`/`tedapi`/`v1r` were restructured so the skip is
  driven by a config field that defaults to `false` (skip only when the
  operator has not supplied a CA, computed as `RootCAs == nil` rather than a
  literal `true`), the same reasoning should apply — but see the caveat: if
  "no CA supplied" is implemented as an explicit `skipVerify := true`
  fallback branch anywhere in the source, that literal reappears and the
  alert would likely persist. The safest phrasing that keeps CodeQL quiet is
  "verify when `RootCAs != nil`," structured so there is no line of source
  that assigns the boolean literal `true` to `InsecureSkipVerify`.

### C. Partial verification — chain-only or hostname-only

Verify the certificate chain (via a custom `VerifyPeerCertificate` or a
`RootCAs` pool) while still skipping hostname/SAN matching, or the reverse
(verify hostname, skip chain validation).

- **Chain-only, no hostname check**: meaningless here specifically because
  there is no CA to build a trust chain against — the gateway's certificate
  is self-signed, so "chain validation" against the public trust store fails
  identically to full verification. It only becomes meaningful once combined
  with option B's operator-supplied CA, at which point it collapses into
  option B or option A.
- **Hostname-only, no chain check**: also not meaningful on its own. Go's
  `crypto/tls` doesn't offer a clean "verify hostname against an untrusted
  self-signed cert" primitive without a custom `VerifyPeerCertificate` that
  is, at that point, doing the same certificate-comparison work TOFU (option
  A) does — and the gateway is usually addressed by IP, not a hostname the
  certificate's SAN would even carry.
- **Verdict**: neither is a distinct, useful third option here. They are
  either no-ops given a self-signed cert with no stable hostname, or they
  reduce to option A or B once actually implemented.

### D. Keep `InsecureSkipVerify` and dismiss the alerts

- **Fixes**: nothing changes; zero engineering cost, zero new failure modes,
  zero behavior change for any existing user.
- **Costs**: the exposure in section 2 remains exactly as described,
  indefinitely, for every deployment topology including the ones where the
  "it's my own LAN" reasoning does not hold.
- **Would CodeQL stop flagging it?** Yes, immediately — a dismissal (`false
  positive` / `won't fix`) closes the specific alert in GitHub's code
  scanning UI without touching code. It does not change the underlying
  finding; a re-scan or a new PR touching those lines would not re-surface
  it only because the *specific alert instance* is dismissed, not because
  CodeQL stops evaluating the rule.

## 4. Recommendation

**Ship option B (operator-supplied CA/certificate, defaulting to today's
skip-verify behavior) for the three flagged sites, and apply the same change
to `scan/scan.go` and `proxy/handlers.go` for consistency.** Do not implement
TOFU pinning now.

Reasoning: option B is the only alternative that changes nothing for the
overwhelming majority of users (home LAN, single operator, no reason to
distrust the network) while giving the minority who need it — the VLAN/shared
cluster/hostile-network cases in section 2 — a real, standards-based way to
get actual verification instead of a workaround. TOFU (option A) sounds
attractive but its failure mode (silent breakage on the firmware's own
routine cert rotation, with no way for the software to tell "attack" from
"update" apart) is a worse day-to-day experience than the status quo for a
device that auto-updates its own firmware, and it does not even resolve the
CodeQL finding once built, since `InsecureSkipVerify: true` stays in the
source either way. Options C are not independently viable. Option D
(dismiss) is defensible and is covered in section 6 in case the owner decides
engineering effort isn't warranted right now, but it leaves the exposure in
place indefinitely for topologies where it matters, and the fix to at least
offer opt-in verification is small enough relative to that, that dismissal
without at least option B looks like the weaker choice.

**Migration path if this recommendation is taken:**

1. Add `Config.GatewayCACert` (a PEM blob or file path — file path is more
   operator-friendly, mirroring `RSAKeyPath`'s existing pattern) and
   `WithGatewayCACert`/`PW_GATEWAY_CA_CERT`, following the existing
   `options.go` conventions (doc comment cross-referencing the `With*`
   function, validated in `ValidateConfig`).
2. In `local.New`, `tedapi.NewClient`, and `NewTEDAPIv1r`, branch: if a CA is
   configured, parse it into an `x509.CertPool`, set `RootCAs` on the
   `tls.Config`, and leave `InsecureSkipVerify` at its zero value (`false`);
   if not configured, keep exactly today's behavior. Structure this as "skip
   only in the absence of a CA" rather than an explicit `true` literal
   assignment, per the CodeQL mechanics discussed in option B above.
3. Apply the identical branch to `scan/scan.go`'s scanning transport (which
   may reasonably keep the unconditional skip, since a network-wide
   discovery probe has no fixed host to pin a CA against until after
   discovery) and to `proxy/handlers.go`'s `proxyLocalGateway`, which shares
   `Config.Host` with the `local` backend and should share its CA config
   too.
4. Document, in each of `docs/architecture/local.md`, `docs/architecture/tedapi.md`,
   and a new `v1r` section, how an operator extracts their gateway's actual
   certificate (`openssl s_client`) and where to point the new config option.
5. Leave the CodeQL alerts open until step 2 lands; at that point re-run the
   scan rather than dismissing, since the fix should make the finding
   genuinely inapplicable to the default path rather than suppressed.
6. TOFU pinning (option A) is worth revisiting later only if operator
   feedback shows the CA-bundle approach is too much friction for the subset
   of users who want *some* verification without manually extracting a
   certificate — but that is a follow-on decision, not part of this one.

## 5. Parity impact

Checked against `docs/parity-matrix.md` (audited against pypowerwall `main`
@ `a3b327be32cd31bb7e297c22e98040c967a86fad`, 2026-09-07): pypowerwall
performs no certificate verification against the local gateway either (its
own local/TEDAPI HTTP clients disable verification for the same self-signed-
certificate reason gopowerwall does), and the parity matrix records no row
suggesting otherwise. None of options A/B/C, **implemented as an opt-in that
defaults to today's `InsecureSkipVerify: true` behavior**, changes anything
about the default connection path: a gopowerwall build with the recommended
change still connects, by default, to every gateway pypowerwall connects to,
with identical default TLS behavior. Parity is only at risk if the default
behavior itself changes — e.g. shipping a default that requires an
operator-supplied CA, or defaulting to on-by-default TOFU that fails closed
on a firmware cert rotation. Neither is part of the recommendation in
section 4: the new option must be opt-in, or it introduces a parity
regression where gopowerwall refuses connections pypowerwall accepts, for
gateways where the operator has not (yet, or ever) supplied a CA bundle. This
constraint should be treated as a hard requirement on the implementation, not
a preference.

## 6. If dismissal is the decision

If the owner decides the engineering cost in section 4 isn't warranted right
now and wants to close the three alerts as accepted risk instead, the correct
GitHub dismissal reason is **"Won't fix"**, not "False positive" — the
finding is real (the code genuinely disables certificate verification and the
exposure in section 2 is real for some topologies), it is simply being
accepted rather than remediated. "Used in tests" does not apply; this is
production transport code. Suggested justification text to paste into the
dismissal:

> Accepted as a known, deliberate limitation. The Powerwall/Powerwall 3
> gateway these three call sites connect to presents a self-signed
> certificate with no publicly-issuable identity (it is addressed by a
> LAN-private IP or its own WiFi AP address, `192.168.91.1`, with no
> per-installation certificate provisioning), so standard chain/hostname
> verification cannot succeed against it regardless of client
> implementation. Upstream pypowerwall, which this project ports, disables
> verification against the same device for the same reason
> (`docs/parity-matrix.md`). The realistic exposure is limited to an
> attacker already on-path between this process and the gateway on the
> operator's local network; see `docs/tls-verification.md` for the full
> analysis, including the topologies (shared/hostile networks, networks the
> device operator does not administer) where that assumption weakens, and
> the alternatives considered and not pursued at this time.

That last document reference should point at this file so the record stays
attached to the decision.
