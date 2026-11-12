# Calls (phase 4: PC to PC)

**Signaling** runs through the api and the existing WebSocket. Events: `call_incoming`, `call_answered`, `call_ended`, `call_hold`, `call_transferred`. REST: `POST /calls`, `/calls/{id}/answer|hangup|reject|hold|transfer`, `GET /calls`, `/calls/{id}/token`, `/calls/ice`.

**Media path.** Each participant opens one WebRTC leg to the `media` service (offer/answer over HTTPS at `/media/calls/{id}/offer`, non-trickle ICE). `media` forwards the RTP of one leg to the other (Opus, SRTP/DTLS on both legs). Relaying instead of peer-to-peer is deliberate: recording (phase 7), SIP bridging and G.711 transcoding (phase 5) all need the server in the path. The api issues a 2-minute JWT per user and call (`aud=media`); `media` only accepts it for that call, and refuses a third leg. When a call ends the api tells `media` to drop the room (`X-Internal-Token`).

**TURN.** The in-house `turn` service (pion/turn) offers STUN and relay on 3478 (UDP and TCP) for PCs that cannot reach media directly. Credentials are time-limited (1 h, HMAC of `TURN_SECRET`), issued by `GET /calls/ice`; nothing is stored.

**Rules.** Calling someone in Do Not Disturb or already in a call is refused immediately (SIP 486 equivalent) and logged as missed for them; calling someone who has not clocked in returns `offline` (voicemail comes with the SIP phase). "In a meeting" rings silently. Both parties show "Busy" during a call and get their previous status back afterwards. A ringing call times out after 45 s. Hold stops sending and playing audio and is relayed to the other party. Transfer is blind: the transferor leaves, the other party is re-routed to the target, who sees the original caller; if the target does not answer, the call ends.

**Known limits (phase 4).** "Away" forwarding to a delegate extension and voicemail need configuration/storage added with the SIP phase; DTMF and external numbers also arrive then. Browsers without a microphone connect listen-only. Calls are logged in the `calls` table (CDR); recording is phase 7.

**Networking.** `media` needs real UDP: dev publishes 10000-10200/udp and advertises `PUBLIC_IP` (127.0.0.1 locally). In production set `PUBLIC_IP` to the server address, open 10000-10200/udp and 3478 + 49152-49200/udp on the firewall, and prefer `network_mode: host` for `media` (see docs/deployment.md when written in phase 7).

**Tests.** `go test ./media ./turn ./pkg/turncred` (real WebRTC relay between two in-process peers, real TURN allocations), `make e2e` (23 signaling checks plus 22 chat checks against the running stack).

## Desk phones (phase 5)

**Codec decision.** The PC leg uses Opus only when both parties are PCs. As soon as a SIP phone is in the call, `media` makes the PC leg negotiate **G.711 mu-law** (browsers support it natively) and relays RTP between the two legs **without transcoding**. This keeps the media server free of C codecs and adds no latency, at the cost of narrow-band audio for a PC in a call with a phone (the phone is narrow-band anyway). A real Opus to G.711 transcoder can be added later in `media` if wideband PC quality on phone calls is required.

**Media anchoring.** Every phone leg is a UDP port of the media server (`POST /legs` with the internal token), so phone-to-phone calls are anchored too (needed for call recording in phase 7). Phones are accepted at the source address of their first RTP packet.

**Signaling.** The `sip` service is a registrar plus call controller. Calls placed from a phone are routed by the api (same rules as PC calls). Calls to a user with registered phones are forked by `sip` on a Redis command from the api (`originate`, `pc_answered`, `ended`). SIP dialogs use the observed source address, which makes phones behind NAT reachable.

**Tests.** `make e2e-phones` runs a real SIP softphone (sipgo) and a real WebRTC client (pion) against the dev stack: digest registration (SHA-256 and MD5), phone to PC and PC to phone with audio in both directions, forking and CANCEL, DND/unknown/offline, phone to phone, provisioning by MAC. `go test ./media` covers the phone-to-browser bridge in process.
