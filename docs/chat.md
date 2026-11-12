# Chat and presence (phase 3)

**Transport.** Clients open `wss://<host>/api/ws` and send `{"type":"auth","token":"<access JWT>"}` as the first frame (tokens never appear in URLs). The api publishes every event to Redis (`eximphone:events`); each api instance delivers to its local sockets, so the api can scale horizontally.

**Events (server to client).** `ready`, `presence`, `message`, `receipt`, `typing`, `message_deleted`, `conversation`, and `kick` (internal: closes a user's sockets on logout). Client to server: `typing`, `delivered`.

**Presence.** Redis hash `presence` (user id to status). Statuses: on, busy, meet, away, dnd; absence of a key means offline. Logout and the worker midnight closure remove the entry and broadcast it.

**Messages.** Bodies are sealed with AES-256-GCM (`MESSAGE_KEY`, kept outside the database; the conversation id is bound as additional authenticated data). Rows are never physically deleted: a trigger blocks `DELETE`, user deletion sets `deleted_at` and is audited. Search decrypts the user's last 1000 messages in the api because ciphertext cannot be matched in SQL.

**Attachments.** PDF, PNG and JPEG only, 20 MB maximum, type detected by content sniffing. Stored in the Garage bucket `attachments`; downloads are limited to conversation members.

**Operations.** Back up `MESSAGE_KEY` separately from the database: without it archived messages cannot be read. The end-to-end encryption mentioned in the spec is a future step; the schema keeps the body opaque to the server logic so it can be added.
