# 0003. SIP accounts: digest authentication with sealed secrets

**Status:** accepted (phase 5)

**Context.** SIP digest authentication needs the server to compute a hash over the password (or a pre-hash of it), and phones must receive their password through auto-provisioning. Passwords must never be stored in clear.

**Decision.** Each phone gets its own account with a generated 24 character password, shown once to the administrator. The password is stored AES-256-GCM sealed (`SIP_KEY`, the account name is bound as additional data) so that `sip` can verify digests and `provisioning` can render configuration files. The registrar offers SHA-256 (RFC 8760) and MD5 challenges together; nonces are stateless (timestamp + HMAC) so any instance can validate them. Failed authentications are counted per source IP.

**Alternatives.** Storing only HA1 hashes (cannot render provisioning files); one shared password per extension (no per-device revocation).

**Consequences.** Losing `SIP_KEY` means re-creating every device. MD5 remains enabled for old phones; disable it once the fleet supports SHA-256. Digest replay within the 2 minute nonce lifetime is not tracked (`nc` is not stored): SIP over TLS is the mitigation.
