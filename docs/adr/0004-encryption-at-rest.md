# 0004. Encryption at rest with application-level keys

**Status:** accepted (phases 3 and 7)

**Context.** Messages, attachments and call recordings are sensitive and subject to retention rules. A database or storage dump must not reveal them.

**Decision.** Encrypt in the application with AES-256-GCM before data reaches PostgreSQL or the object store. Separate keys per purpose, all supplied through the environment and never stored in the database: `MESSAGE_KEY` (messages, attachments), `RECORDING_KEY` (recordings), `SIP_KEY` (device secrets). The row or object identifier is bound as additional authenticated data, so a ciphertext cannot be moved to another row or conversation.

**Alternatives.** Disk or volume encryption only (does not protect against a logical dump, kept as a complement); a KMS or HSM (the right target for production, the key interface is a single `Box` type to swap).

**Consequences.** Messages cannot be searched in SQL: search decrypts the user's most recent messages in the api (acceptable at this size). Keys must be backed up separately from the database and rotated with a re-encryption job (not written yet). End-to-end encryption is not implemented; the server can read messages by design (archiving requirement).
