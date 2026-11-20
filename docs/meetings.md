# Video meetings, video calls and screen sharing

Meetings of up to **12 people** with microphone, camera and **screen sharing**, plus **one-to-one video calls**, all inside the browser or the desktop app, on the bank's own servers.
A participant can also ask to **take control of a shared screen** (see "Remote control"), when the owner uses the desktop app.

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
- `services/media/meeting_rc_test.go`: the remote-control rules (consent, one controller, input only from the controller to the owner and only during a grant, rate limit, revocation when the share stops or somebody leaves, expiry, audit notifications).
- `tests/e2e/meetings.mjs` (40 checks): invitations, tokens, the WebSocket through nginx, declining, leaving reported by the media service, ending, auto-close, video calls.
- The camera, microphone and screen picker need a real browser with permission: they could not be exercised in the automated browser, which blocks capture devices. Try them with two people before the pilot.

## Remote control of a shared screen

A participant can ask to **move the mouse and use the keyboard** on a screen that another participant is sharing, typically for IT support.
This is the most sensitive feature of the platform, so it is built to be impossible to use without the knowledge of the person who owns the computer.

**Who can be controlled.** Only a person who uses the **desktop app** (Windows) and shares a **whole screen** (not a window). In a web browser, or when sharing a window, the *Request control* button never appears.

**The flow.**
1. The controller clicks *Request control* on the shared screen of the owner.
2. The owner's desktop app opens a **native Windows dialog** (not a web page, so it cannot be imitated or answered by the page). The default answer is *Refuse*. It says who asks and what they will be able to do.
3. If the owner allows, a **red banner** stays on top of every window of the shared screen for the whole session ("X is controlling your screen") with a *Stop* button. The shortcut **Ctrl+Alt+Shift+X** stops it at any time, even if the app is not in front. Everybody in the meeting sees who controls which screen.
4. The controller clicks and types on the shared picture; the events travel over the meeting WebSocket to the media service, which forwards them **only from that controller to that owner**, only while the session is active.

**Safeguards (enforced by the desktop app and by the server, not by the page):**
- the session needs the owner's explicit consent **each time**; nothing is remembered;
- it ends by itself after **1 hour**, when the screen share stops, when either person leaves or loses the connection, or when either presses *Stop*; the keys and buttons still held are released;
- one controller per screen, and a person cannot be both controller and controlled; a request not answered within 30 seconds expires;
- the server validates every event (known kinds, coordinates clamped to the screen, short key names) and **rate-limits** the flow (400 events per second);
- the **Windows key is never injected** (no Start menu, no Win+R, no lock shortcut); the lock screen, UAC prompts and Ctrl+Alt+Del cannot be controlled by design;
- the web page cannot start a session or inject anything by itself: the desktop app only accepts the commands from the Exim Connect page, and only after the native consent;
- **audit log**: `remote_control_started` and `remote_control_stopped` (who controlled whose screen, in which meeting, why it stopped and for how many seconds).

**Smoothness.** Pointer moves are merged to one per display frame (the latest position), the pointer is captured during a drag (it keeps working outside the tile), and the controller's own cursor is drawn locally at once since the owner's real cursor only comes back with the video. While a screen is controlled the owner's app raises the share to 30 images per second, 4 Mbit/s and "motion" encoding, and restores the text-friendly settings (15 images/s, 2 Mbit/s) afterwards. A floating bar on the controlled screen offers *Full screen* and *Stop control*. Buttons and keys still held are released when the controller's window loses focus.

**Typing.** Characters are typed from the character the controller produces (not the physical key), so it works between different keyboard layouts (AZERTY/QWERTY); shortcuts such as Ctrl+C or Ctrl+A work. Keys that have no equivalent on the owner's layout are ignored.

**Under the hood.** The desktop app (Electron) does the injection with a few Windows calls (`SetCursorPos`, `mouse_event`, `keybd_event`) through the `koffi` library (MIT, loaded only when a session is accepted); the logic is in `desktop/remote.js` and is tested without touching the machine (`npm test` in `desktop/`, 15 tests). The installer must be rebuilt (`npm run dist`, from a short path such as `C:\exim-desk`) for the feature to reach the users' PCs.

**Not covered / limits.** Only the primary use case is covered: one controller, one screen. There is no file transfer and no clipboard sharing. A multi-screen owner chooses which screen to share in the app's picker. The bank should decide who may be allowed to request control (today any participant can ask; the owner decides) and review the `koffi` dependency as part of its usual software review.

## Next

- Simulcast/bandwidth adaptation, active-speaker highlight, meeting recording, chat inside a meeting, scheduled meetings with calendar invitations, a waiting room.
- Restricting remote-control requests to some roles (for example IT support), and a meeting-level switch to disable it.
