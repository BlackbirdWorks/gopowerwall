# v1r mode

Implementation: `backend/tedapi/v1r.go` (`TEDAPIv1r`), wired into the same
`backend/tedapi.Client`/`PyPowerwallTEDAPI` facade described in [tedapi.md](tedapi.md) via
`Client.SetV1rTransport`. This is the RSA-signed transport Powerwall 3 gateways use for
TEDAPI over the wired LAN/vendor subnet, as opposed to plain TEDAPI's WiFi-AP-only
transport.

## Transport

Two HTTP endpoints on the gateway, both self-signed HTTPS like every other local mode:

- `GET/POST https://<host>/api/login/Basic` and `https://<host>/tedapi/din` — plain
  Bearer-token calls, same shape as local mode's login.
- `POST https://<host>/tedapi/v1r` — an RSA-signed `combined.RoutableMessage` envelope, the
  actual v1r payload channel.

## Auth model

Two independent credentials are required, layered together:

1. **Customer password** (`--password`/`PW_PASSWORD`, or the last 5 characters of
   `--gw_pwd` if `--password` is omitted — see `Powerwall.connectLocal` in
   `powerwall.go`). `TEDAPIv1r.Login` POSTs this to `/api/login/Basic` exactly like local
   mode's login, and receives a Bearer token used for the `/tedapi/din` DIN lookup.
2. **An RSA private key** (`--rsa_key_path`, default `./tedapi_rsa_private.pem`; PEM,
   PKCS#1 or PKCS#8). `NewTEDAPIv1r` derives a SHA-256 fingerprint of the DER-encoded public
   key (`KeyFingerprint`) and uses the private key to RSA-PKCS#1v1.5/SHA-512-sign a TLV
   payload (`BuildTLVPayload`) built from the gateway's DIN, a signature type/domain tag,
   and an expiry timestamp (`expirationOffset`, 12 seconds ahead). That signature is
   attached to every `/tedapi/v1r` request as `RoutableMessage.SignatureData`. A `401`/`403`
   response triggers exactly one re-login-and-retry, matching local mode's pattern.

**The key must already be registered with the gateway before this works.** If the gateway
doesn't recognize the key's fingerprint, `PostV1r` returns
`backend.ErrUnknownKeyID: key fingerprint <fp>` (decoded from a
`MessageFault_E_MESSAGEFAULT_ERROR_UNKNOWN_KEY_ID` response). gopowerwall's `register`
CLI subcommand, which pypowerwall would use to drive this registration, currently only
prints a status line and performs no registration request — see
[MISSING.md](../../MISSING.md#cli-subcommands-that-only-print-guidance-text). In practice
the key pair and its gateway registration need to come from elsewhere today.

## What it can read/write

Everything plain TEDAPI mode can (see [tedapi.md](tedapi.md)), routed through the signed
`/tedapi/v1r` channel instead of the unsigned `/tedapi/v1` one whenever `v1r != nil` on the
`Client` — `execGraphQL`, `GetConfig`'s `readV1rConfig` path, `ScheduleMaxBackup`,
`CancelMaxBackup`, and `GetBackupEvents` all prefer the v1r transport when it's attached.
`Client.Connect` also uses `TEDAPIv1r.GetDin` as its connectivity check in this mode instead
of a plain config fetch.

## When to choose it

Use `v1r` specifically for a Powerwall 3 gateway reachable over the wired LAN or vendor
subnet (not the WiFi AP), when you have both the customer password and a gateway-registered
RSA key pair. If you only need WiFi-AP TEDAPI access and don't have a registered key,
`tedapi` mode is simpler. If you have full LAN access and the customer password but don't
need TEDAPI-specific fields, plain `local` mode has no RSA key requirement at all.
