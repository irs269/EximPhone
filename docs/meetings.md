# Video meetings, video calls and screen sharing

Meetings of up to **12 people** with microphone, camera and **screen sharing**, plus **one-to-one video calls**, all inside the browser or the desktop app, on the bank's own servers.
Remote control of a shared computer is **not** part of this stage (see "Next").

## Using it

- **Meetings** (side bar, *Meetings*): *New meeting* → optional title, pick up to 11 colleagues → *Start the meeting*. Invited people get a ringing invitation (accept or decline). A meeting in progress is listed under *In progress* for everyone invited, with a *Join* button. Anyone in a meeting can *Invite* more colleagues.
- **Video call**: the *Video call* button in the directory or in a chat header rings the other person; if they decline, the call ends for you as well.
- **In the room**: mute, camera on/off, **share screen** (the browser asks which screen or window; its own *Stop sharing* button also works), participant list, invite, *Leave*, and for the host *End for everyone*. A shared screen takes the stage and the participants move to a side column. When the camera is off, the person's initials are shown but they are still heard.
- If the microphone or the camera is refused or missing, the person joins anyway and can listen and watch; the room says so.

## How it works

```
browser A ──┐  publish connection (A offers: mic, camera, screen)      ┌── subscribe connection (server offers what the others send) ──> browser B
browser B ──┼──────────────> media service (SFU) ───────────────────────┤
browser C ──┘               forwards RTP, never decodes it              └──> browser C
          one WebSocket per participant for signalling:  /media/meetings/{id}/ws
```

- Each participant has **two WebRTC connections** to the media service: one to *publish* (the browser makes the offers, also when it starts or stops sharing) and one to *subscribe* (the server makes the offers whenever someone arrives, leaves or shares). Using two connections avoids offer collisions.
- The service reads each published track once and copies the packets to every other participant: **no decoding, no mixing**, so the cost grows with the number of participants rather than with the picture quality. Video is VP8, audio Opus. Keyframes are requested when someone joins and when a viewer reports a lost picture.
- Streams are named `<user>:<cam|screen>` so the browser knows which tile a stream belongs to. The camera stream carries the person's audio and video; the screen is a separate video stream.
- Signalling goes over one WebSocket (through nginx); the first frame carries a **2-minute token** issued by the api for one user and one meeting. The media itself uses the same single UDP port as calls (ICE UDP mux, `10000/udp`), with TURN as the fallback.
- The api (`/api/meetings...`) only handles invitations and records who took part; the media service reports departures to the api. A meeting ends when the host ends it or when nobody is left. Audit log: `meeting_created`, `meeting_ended`.

## API

| Method and path | Purpose |
|---|---|
| `POST /meetings` | `{title?, kind: "meeting"|"video_call", invitees}`; a video call has exactly one invitee, a meeting up to 11 |
| `GET /meetings?view=active|recent` | my meetings |
| `GET /meetings/{id}` | details and participants (404 for people not invited) |
| `POST /meetings/{id}/token` | join: marks me as joined and returns the media token |
| `POST /meetings/{id}/invite`, `/decline`, `/leave`, `/end` | the rest of the life cycle (`end` is host only) |

Real-time events: `meeting_invite` (rings), `meeting_update`, `meeting_ended`.

## Limits and sizing

- **12 simultaneous participants** per meeting (the 13th gets "meeting is full"). Many meetings can run at the same time.
- A participant uploads their own streams once (camera about 0.9 Mbit/s, screen up to 2 Mbit/s) and downloads the streams of every other participant. A 12-person meeting with 12 cameras means about 10 Mbit/s down per person and 130 Mbit/s through the server; plan the VPS and its uplink accordingly (a handful of meetings of 4 to 6 people is comfortable on a small server).
- There is no bandwidth adaptation yet (no simulcast): a participant on a weak connection receives the full quality of the others.
- Recording of meetings is not implemented.

## Tests

- `services/media/meeting_test.go`: real WebRTC clients against the SFU (three participants exchanging audio and video, a late joiner, screen sharing next to the camera, stopping the share, leaving, forged tokens, the 12-person limit).
- `tests/e2e/meetings.mjs` (30 checks): invitations, tokens, the WebSocket through nginx, declining, leaving reported by the media service, ending, auto-close, video calls.
- The camera, microphone and screen picker need a real browser with permission: they could not be exercised in the automated browser, which blocks capture devices. Try them with two people before the pilot.

## Next

- **Remote control** of a shared screen: this cannot be done by a web page. It needs the desktop app (Electron) to inject mouse and keyboard events after the viewer's request is **explicitly accepted by the owner for each session**, with a visible banner, an instant "stop" and an audit record. Planned as stage 2.
- Simulcast/bandwidth adaptation, active-speaker highlight, meeting recording, chat inside a meeting, scheduled meetings with calendar invitations, a waiting room.
