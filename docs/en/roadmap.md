# Exim Connect — Roadmap and external e-mail

*What remains before production, what can come next, and what we need to send and receive e-mail outside the bank.*

---

## 1. Where do we stand?

| Area | Status |
|---|---|
| Attendance, messaging, calls, announcements, HR, audit | **Complete and tested** |
| **Internal** mail (To, Cc, Bcc, drafts, threads, attachments, search) | **Complete and tested** |
| **External** mail (write to the outside, receive replies) | **Built and tested** against a simulated mail server. **Only the connection to the bank's real mail server is missing** (§ 2) |
| Desk IP phones (SIP) | **Complete**; to be validated with the bank's actual phone models |
| Video meetings up to 12, screen sharing | **Complete and tested** |
| Remote control (PC application) | **Complete**; smoothness and ergonomics are being tuned with the first users |
| PC application (Windows installer) | **Available**; **code signing** is still missing for production (§ 3) |
| Test environment (dedicated server) | **In place**: test certificate, fictitious directory, restricted access |

---

## 2. Sending external e-mail: what we need

### 2.1 How it works

Exim Connect **is not a mail server** and is not meant to replace one. To write to the outside, it relies on **the bank's existing mail server**, which it uses as a gateway:

```
   OUTGOING                                         INCOMING
   Employee ─► Exim Connect ─► the bank's           Internet ─► the bank's
                               SMTP relay                       mail server
                                    │                                │ copy rule
                                    ▼                                ▼
                                Internet                      service mailbox
                                                                (IMAP)
                                                                     │ polled every 30 s
                                                                     ▼
                                                             Exim Connect files each
                                                             message in the mailbox of
                                                             the employee concerned
```

**Why go through the existing server?** Messages leave with the **bank's identity** (DKIM signature, SPF, DMARC), go through the usual **antivirus, anti-spam and security gateway**, are **archived** like any e-mail of the bank, and the reputation of the sending address is not put at risk.

### 2.2 What the bank must provide

To be filled in by the messaging team (IT / Exchange administrator or equivalent).

#### A. To **send** (SMTP relay)

| No. | Information or action | Example | Why |
|---|---|---|---|
| A1 | **Name or address** of the sending server (SMTP relay) | `smtp.exim.example` | where Exim Connect drops the messages |
| A2 | **Port** and **security mode** | `587` + STARTTLS, or `465` + TLS | encryption of the link |
| A3 | **Sending account** (login + password) **or authorisation by IP address** of the Exim Connect server | `svc-eximconnect` | relay authentication |
| A4 | **Right to send on behalf of employees** ("Send As"), **or** a single sending address | `noreply-connect@exim.example` | see decision 1 (§ 2.3) |
| A5 | **Certificate of the bank's internal authority** (.pem file) | `ca-exim.pem` | so the encrypted link can be verified |
| A6 | **Maximum message size** of the relay | 25 MB | must be ≥ the allowed attachments |
| A7 | **Firewall opening**: from the Exim Connect server to the relay, on the chosen port | — | the link must be allowed |

#### B. To **receive** (service mailbox)

| No. | Information or action | Example | Why |
|---|---|---|---|
| B1 | **A dedicated mailbox** for Exim Connect | `connect-inbound@exim.example` | Exim Connect polls it |
| B2 | **IMAP server**, port and security mode | `imap.exim.example`, `993` + TLS | reading the mailbox |
| B3 | **Account** (login + password) for this mailbox | `svc-eximconnect-imap` | authentication |
| B4 | **A copy rule**: the mail of employees who use Exim Connect is **copied (Bcc) or forwarded** to this mailbox | transport rule or alias | this is what feeds the mailbox |
| B5 | **Firewall opening**: from the Exim Connect server to the IMAP server | — | the link must be allowed |

> Exim Connect finds the recipient from the addresses in the message headers (To, Cc, `Delivered-To`, `X-Original-To`). **One single account** is therefore enough; no employee password is stored.

#### C. In the **corporate directory**

| No. | Information or action | Why |
|---|---|---|
| C1 | The **`mail`** attribute (e-mail address) is **filled in and correct** for each employee | used to recognise incoming messages and to build the sender |
| C2 | Any **aliases** (former address, short address) | avoid messages "with no recognised recipient" |

#### D. Security and compliance

| No. | Point | To confirm |
|---|---|---|
| D1 | Domain **SPF**: is the Exim Connect server allowed to send? | only needed if the sender is not the relay's |
| D2 | **DKIM / DMARC**: signing is done by the usual relay | confirm |
| D3 | **Archiving / legal retention** of outgoing and incoming mail | does it go through the existing relay? |
| D4 | **Data loss prevention (DLP)** and antivirus gateway | does it apply to the existing relay? |
| D5 | **Allowed or forbidden domains** (allow list, block list) | to be configured if needed |

### 2.3 Decisions to make

| Decision | Possible choices | Our advice |
|---|---|---|
| **Decision 1: who appears as the sender?** | **The employee's address** (`first.last@exim.example`): more natural, but the sending account must have the "Send As" right. **A single address** (`noreply-connect@…`) with "Reply-To" = the employee: simpler to authorise, less natural. | **The employee's address** if the messaging team allows it: customers recognise the bank and replies come back to the right person. |
| **Decision 2: who may write to the outside?** | All employees, or only some services or roles. | Everybody to begin with, with the **traceability** already in place (every send is logged, without its content). |
| **Decision 3: attachment size** | 25 MB per message today (20 MB per file). | Align with the relay's limit (A6). |

### 2.4 What Exim Connect does on its side

Once the information is received, **we**:

1. enter the service settings (`MAIL_SMTP_*`, `MAIL_IMAP_*`, certificate) without changing the code;
2. restart the API service;
3. test: sending to an external address, receiving a reply, attachment, accents, Cc/Bcc, delivery failure;
4. run the acceptance tests (§ 2.5) with two or three pilot employees.

**Already covered by the tests** (22 checks): UTF-8 MIME messages with attachments, Bcc recipients kept out of the visible envelope, retry after failure (1 min, 5 min, 30 min, 2 h, 6 h), a "Delivery failure" message to the sender after a permanent refusal, a colleague's address delivered **internally** without going through the relay, incoming mail filed in the right mailbox with its attachments, HTML mail converted to safe text, duplicates ignored, replies grouped in the same thread.

### 2.5 Acceptance tests

| # | Test | Expected result |
|---|---|---|
| 1 | Send a message to an **external** test address | it arrives, sender and subject correct, accents readable |
| 2 | Send with a 10 MB **attachment** | it arrives intact |
| 3 | Put an address in **Bcc** | it receives the message, the others do not see it |
| 4 | **Reply** from the external address | the reply appears in the employee's mailbox, in the same thread, with the **External** label |
| 5 | Write to a **non-existent address** | a "Delivery failure" message reaches the sender |
| 6 | Write to a **colleague** using their full address | delivered internally, immediately |
| 7 | Send to an address **blocked** by the gateway | the failure is reported cleanly |
| 8 | Check **SPF / DKIM / DMARC** (headers of the received message) | all "pass" |

### 2.6 Indicative timeline

| Step | Who | Estimated duration |
|---|---|---|
| Collecting the information (§ 2.2) and decisions | IT / messaging | 2 to 5 working days |
| Firewall openings, account creation, copy rule | IT / security | 1 to 5 days depending on the change process |
| Configuration and first tests | Exim Connect team | 1 day |
| Acceptance with pilot users | IT + pilots | 1 to 2 days |

### 2.7 Current limits of mail

Mail is **plain text** (no formatting or images in the body). Not yet offered: **signatures**, **out-of-office replies**, **custom folders**, **shared mailboxes**, **calendar invitations**. **Spam filtering** is left to the bank's server. This version uses **a single service mailbox** for reception, rather than one mailbox per employee.

---

## 3. Before going into production

| # | Action | Detail | Owner |
|---|---|---|---|
| 1 | **Server certificate** | certificate from the bank's internal authority, on the final name | IT |
| 2 | **Real directory** | connect over LDAPS, read-only service account, import of attributes (name, department, site, extension, `mail`, manager) | IT |
| 3 | **Real IP ranges** | replace the demonstration ranges with each branch's networks | IT / network |
| 4 | **Hours and days** | check hours, tolerance and time zone per site | HR |
| 5 | **Roles** | appoint IT administrators, HR, managers, communication, auditors, heads of service | IT / HR |
| 6 | **Installer signing** | code-signing certificate to avoid the Windows warning | IT |
| 7 | **Scheduled backups** | script provided (encrypted): schedule it daily, copy off the server, test the restore monthly | IT |
| 8 | **Key custody** | `MESSAGE_KEY`, `RECORDING_KEY`, `SIP_KEY`, backup passphrase: a vault separate from the server | IT / security |
| 9 | **Firewall** | open only HTTPS and the required telephony and media ports; restricted administration access | network |
| 10 | **Monitoring** | alerts on service health, disk space, backups | IT |
| 11 | **Retention periods** | attendance, audit, recordings: legal durations | legal / HR |
| 12 | **Real load test** | on the production server, with the real number of users | project team |
| 13 | **Training and guide** | present the manual to employees, appoint relays | HR / IT |
| 14 | **Business acceptance** | 2 to 3 weeks with a pilot group (one branch + head office) | everyone |

---

## 4. Possible evolutions

### Short term (1 to 3 months)

| Evolution | Benefit |
|---|---|
| **Nightly directory synchronisation** (arrivals, departures, account deactivation) | accounts always up to date without manual action |
| **Public holiday and leave calendar** | no false absences; accurate HR reports |
| **Manager notification** (e-mail or message) when someone forgets to clock out | follow-up without opening the dashboard |
| **Limit who can request remote control** (by role or department) | a stricter security framework |
| **Smoother remote control**: feedback from the first trials, image quality and responsiveness | comfort of use |

### Medium term (3 to 9 months)

| Evolution | Benefit |
|---|---|
| **Strong authentication (2 factors)** and **single sign-on (SSO)** | stronger security |
| **Voicemail** and call forwarding | complete telephony |
| **Connection to a telephone operator** (outside calls) | a single phone |
| **Richer mail**: signatures, out-of-office replies, custom folders, shared mailboxes | everyday use close to a desktop client |
| **Meeting recording and summary**, chat inside the meeting | record and time saved |
| **File transfer and clipboard** during remote control | more effective remote assistance |
| **Multi-monitor support** for remote control | two-screen workstations |

### Longer term

| Evolution | Benefit |
|---|---|
| **Mobile application** (iOS, Android) | reachable away from the desk |
| **Wider request portal** (leave, certificates, expense claims) built on tickets | a single counter |
| **Usage statistics** (IT dashboards) | steering |
| **Swahili version** and other languages | accessibility |

---

## 5. Known limits

- A day without a clock-in counts as an **absence**: weekends and public holidays are not distinguished (see *public holiday calendar*).
- **Remote control** only works on **Windows**, from the **PC application**, for a **whole screen**.
- The PC application is not **signed** yet: Windows asks for confirmation at installation.
- External mail assumes **the bank's mail server** and the copy rule described in § 2.2.
- Load tests were done on a small server: a **measurement on production hardware** is still to be made.
