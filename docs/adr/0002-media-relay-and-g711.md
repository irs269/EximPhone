# 0002. Media relayed by our server, G.711 between PCs and phones

**Status:** accepted (phases 4 and 5)

**Context.** PCs speak WebRTC (Opus, DTLS-SRTP); desk phones speak SIP with G.711 over RTP/SRTP. Calls must be recordable for compliance, and phones are often behind NAT.

**Decision.**
1. Every call goes through the `media` service (a relay, not peer-to-peer). Each party has one leg; `media` forwards RTP between legs. This makes recording, SIP bridging and NAT latching possible.
2. PC to PC calls keep Opus. As soon as a phone is in the call, the PC leg negotiates G.711 mu-law and `media` forwards the packets **without transcoding**.
3. Phone legs are accepted at the source address of their first RTP packet (symmetric RTP), and SIP dialogs use the observed source address instead of `Contact`.

**Alternatives.** Peer-to-peer WebRTC (no recording, no bridging). A real Opus to G.711 transcoder (needs libopus through cgo; more CPU and latency; can be added later in `media` behind the same leg interface).

**Consequences.** One extra network hop per call and server bandwidth proportional to calls (about 100 kbit/s per direction for G.711). Narrow-band audio for a PC talking to a desk phone.
