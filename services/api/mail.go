package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
)

// Internal mail between the employees of the platform.
//
// A message has one sender and recipients (to / cc / bcc). Each participant owns a row in mail_boxes holding
// the folder (inbox, sent, drafts, archive, trash), the read flag and the star, so one person's filing never
// affects another's. Subject and body are sealed at rest; attachments are sealed in the object store.
// Deleting is logical: trash, then "delete forever" hides the message for that user only.
// Messages are grouped into conversations (thread_id) by replying or forwarding.

const (
	mailMaxRecipients = 50
	mailMaxSubject    = 300
	mailMaxBody       = 100000
	mailMaxFiles      = 10
	mailMaxTotal      = 25 << 20
	mailWindow        = 1000 // rows read per listing: grouping and search work on this window
	mailPageSize      = 30
)

var mailBlockedExt = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".msi": true, ".dll": true, ".js": true,
	".vbs": true, ".ps1": true, ".jar": true, ".lnk": true, ".sh": true, ".apk": true, ".hta": true, ".reg": true,
}

func (a *app) mailRoutes(r chi.Router) {
	r.Get("/mail/folders", a.mailFolders)
	r.Get("/mail/messages", a.mailList)
	r.Get("/mail/threads/{id}", a.mailThread)
	r.Post("/mail/drafts", a.mailSaveDraft)
	r.Delete("/mail/drafts/{id}", a.mailDiscardDraft)
	r.Post("/mail/drafts/{id}/send", a.mailSendDraft)
	r.Post("/mail/send", a.mailSendNow)
	r.Post("/mail/batch", a.mailBatch)
	r.Post("/mail/attachments", a.mailUpload)
	r.Get("/mail/attachments/{id}", a.mailDownload)
}

func (a *app) mailSeal(kind string, id int64, text string) ([]byte, error) {
	return a.box.Seal([]byte(text), []byte(fmt.Sprintf("mail-%s:%d", kind, id)))
}

func (a *app) mailOpen(kind string, id int64, sealed []byte) string {
	plain, err := a.box.Open(sealed, []byte(fmt.Sprintf("mail-%s:%d", kind, id)))
	if err != nil {
		return ""
	}
	return string(plain)
}

type mailPerson struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"` // set for people outside the platform (id 0)
}

type mailAttachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
}

type mailItem struct {
	ID             int64      `json:"id"` // latest message of the conversation within the folder
	ThreadID       int64      `json:"thread_id"`
	IDs            []int64    `json:"ids"`
	From           mailPerson `json:"from"`
	To             []string   `json:"to"`
	Subject        string     `json:"subject"`
	Snippet        string     `json:"snippet"`
	Date           time.Time  `json:"date"`
	Count          int        `json:"count"`
	Unread         bool       `json:"unread"`
	Starred        bool       `json:"starred"`
	HasAttachments bool       `json:"has_attachments"`
	Draft          bool       `json:"draft"`
}

// folderPredicate maps a folder name to its SQL condition on mail_boxes (alias b). Constants only.
func folderPredicate(folder string) (string, bool) {
	switch folder {
	case "inbox", "sent", "drafts", "archive", "trash":
		return "b.folder = '" + folder + "'", true
	case "starred":
		return "b.starred AND b.folder NOT IN ('trash','drafts')", true
	}
	return "", false
}

// ---------- folders ----------

func (a *app) mailFolders(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	var inbox, drafts, starred int
	_ = a.st.db.QueryRow(r.Context(), `
		SELECT COUNT(*) FILTER (WHERE folder='inbox' AND NOT is_read),
		       COUNT(*) FILTER (WHERE folder='drafts'),
		       COUNT(*) FILTER (WHERE starred AND folder NOT IN ('trash','drafts'))
		FROM mail_boxes WHERE user_id=$1 AND NOT purged`, uid).Scan(&inbox, &drafts, &starred)
	writeJSON(w, 200, map[string]any{"unread_inbox": inbox, "drafts": drafts, "starred": starred, "external": mailCfg.outbound()})
}

// ---------- listing and search ----------

type mailRow struct {
	id, thread, sender int64
	senderName         string
	senderEmail        string
	ts                 time.Time
	read, starred      bool
	subject, body      []byte
}

func (a *app) mailList(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	folder := r.URL.Query().Get("folder")
	if folder == "" {
		folder = "inbox"
	}
	pred, ok := folderPredicate(folder)
	if !ok {
		apiError(w, 400, "unknown folder")
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	cols := `m.subject, ''::bytea`
	if q != "" {
		cols = `m.subject, m.body`
	}
	rows, err := a.st.db.Query(ctx, `
		SELECT m.id, m.thread_id, COALESCE(m.sender_id,0), COALESCE(su.display_name, NULLIF(m.external_name,''), m.external_address, ''), COALESCE(m.external_address,''),
		       COALESCE(m.sent_at, m.updated_at), b.is_read, b.starred, `+cols+`
		FROM mail_boxes b JOIN mail_messages m ON m.id=b.message_id LEFT JOIN users su ON su.id=m.sender_id
		WHERE b.user_id=$1 AND NOT b.purged AND `+pred+`
		ORDER BY COALESCE(m.sent_at, m.updated_at) DESC, m.id DESC LIMIT $2`, uid, mailWindow)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var all []mailRow
	for rows.Next() {
		var x mailRow
		if rows.Scan(&x.id, &x.thread, &x.sender, &x.senderName, &x.senderEmail, &x.ts, &x.read, &x.starred, &x.subject, &x.body) == nil {
			all = append(all, x)
		}
	}
	rows.Close()

	// recipients' names are needed to search and to show "To:" in Sent and Drafts
	ids := make([]int64, len(all))
	for i, x := range all {
		ids[i] = x.id
	}
	names := a.mailRecipientNames(ctx, ids, uid)

	// group into conversations (a draft stays alone), newest first
	type group struct {
		latest    mailRow
		ids       []int64
		unread    bool
		starred   bool
		matches   bool
		latestSet bool
	}
	var order []int64
	groups := map[int64]*group{}
	for _, x := range all {
		key := x.thread
		if folder == "drafts" {
			key = x.id
		}
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
			order = append(order, key)
		}
		if !g.latestSet {
			g.latest, g.latestSet = x, true
		}
		g.ids = append(g.ids, x.id)
		g.unread = g.unread || !x.read
		g.starred = g.starred || x.starred
		if q != "" && !g.matches {
			hay := strings.ToLower(a.mailOpen("subject", x.id, x.subject) + "\n" + a.mailOpen("body", x.id, x.body) + "\n" + x.senderName + "\n" + strings.Join(names[x.id], " "))
			g.matches = strings.Contains(hay, q)
		}
	}
	var picked []*group
	for _, k := range order {
		if g := groups[k]; q == "" || g.matches {
			picked = append(picked, g)
		}
	}
	start := page * mailPageSize
	if start > len(picked) {
		start = len(picked)
	}
	end := start + mailPageSize
	if end > len(picked) {
		end = len(picked)
	}
	more := end < len(picked)
	picked = picked[start:end]

	// bodies only for the page shown
	pageIDs := make([]int64, 0, len(picked))
	for _, g := range picked {
		pageIDs = append(pageIDs, g.latest.id)
	}
	bodies := map[int64][]byte{}
	attach := map[int64]bool{}
	if len(pageIDs) > 0 {
		if br, err := a.st.db.Query(ctx, `SELECT id, body FROM mail_messages WHERE id = ANY($1)`, pageIDs); err == nil {
			for br.Next() {
				var id int64
				var b []byte
				if br.Scan(&id, &b) == nil {
					bodies[id] = b
				}
			}
			br.Close()
		}
		if ar, err := a.st.db.Query(ctx, `SELECT DISTINCT message_id FROM mail_attachments WHERE message_id = ANY($1)`, pageIDs); err == nil {
			for ar.Next() {
				var id int64
				if ar.Scan(&id) == nil {
					attach[id] = true
				}
			}
			ar.Close()
		}
	}
	out := make([]mailItem, 0, len(picked))
	for _, g := range picked {
		l := g.latest
		subject := a.mailOpen("subject", l.id, l.subject)
		item := mailItem{
			ID: l.id, ThreadID: l.thread, IDs: g.ids, From: mailPerson{ID: l.sender, Name: l.senderName, Email: l.senderEmail}, To: names[l.id],
			Subject: subject, Snippet: snippet(a.mailOpen("body", l.id, bodies[l.id]), 140), Date: l.ts, Count: len(g.ids),
			Unread: g.unread, Starred: g.starred, HasAttachments: attach[l.id], Draft: folder == "drafts",
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"items": out, "more": more})
}

func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// mailRecipientNames returns the names of the to/cc recipients of each message (bcc only for the sender).
func (a *app) mailRecipientNames(ctx context.Context, ids []int64, uid int64) map[int64][]string {
	out := map[int64][]string{}
	if len(ids) == 0 {
		return out
	}
	rows, err := a.st.db.Query(ctx, `
		SELECT r.message_id, u.display_name FROM mail_recipients r JOIN users u ON u.id=r.user_id JOIN mail_messages m ON m.id=r.message_id
		WHERE r.message_id = ANY($1) AND (r.kind <> 'bcc' OR m.sender_id=$2)
		ORDER BY r.message_id, CASE r.kind WHEN 'to' THEN 0 WHEN 'cc' THEN 1 ELSE 2 END, u.display_name`, ids, uid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n string
		if rows.Scan(&id, &n) == nil {
			out[id] = append(out[id], n)
		}
	}
	if er, err := a.st.db.Query(ctx, `
		SELECT r.message_id, COALESCE(NULLIF(r.name,''), r.address) FROM mail_external_recipients r JOIN mail_messages m ON m.id=r.message_id
		WHERE r.message_id = ANY($1) AND (r.kind <> 'bcc' OR m.sender_id=$2) ORDER BY r.message_id, r.address`, ids, uid); err == nil {
		for er.Next() {
			var id int64
			var n string
			if er.Scan(&id, &n) == nil {
				out[id] = append(out[id], n)
			}
		}
		er.Close()
	}
	return out
}

// ---------- reading a conversation ----------

type mailMessage struct {
	ID          int64            `json:"id"`
	ParentID    *int64           `json:"parent_id"`
	From        mailPerson       `json:"from"`
	To          []mailPerson     `json:"to"`
	Cc          []mailPerson     `json:"cc"`
	Bcc         []mailPerson     `json:"bcc"`
	Subject     string           `json:"subject"`
	Body        string           `json:"body"`
	Date        time.Time        `json:"date"`
	Unread      bool             `json:"unread"`
	Starred     bool             `json:"starred"`
	Draft       bool             `json:"draft"`
	Mine        bool             `json:"mine"`
	Attachments []mailAttachment `json:"attachments"`
}

func (a *app) mailThread(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	thread, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	trash := r.URL.Query().Get("folder") == "trash"
	rows, err := a.st.db.Query(ctx, `
		SELECT m.id, m.parent_id, COALESCE(m.sender_id,0), COALESCE(su.display_name, NULLIF(m.external_name,''), m.external_address, ''), COALESCE(m.external_address,''), m.subject, m.body, COALESCE(m.sent_at, m.updated_at), m.sent_at IS NULL,
		       NOT bool_and(b.is_read), bool_or(b.starred)
		FROM mail_boxes b JOIN mail_messages m ON m.id=b.message_id LEFT JOIN users su ON su.id=m.sender_id
		WHERE b.user_id=$1 AND NOT b.purged AND m.thread_id=$2 AND ((b.folder='trash') = $3)
		GROUP BY m.id, su.display_name, m.external_name, m.external_address
		ORDER BY COALESCE(m.sent_at, m.updated_at), m.id`, uid, thread, trash)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var msgs []mailMessage
	var ids []int64
	for rows.Next() {
		var m mailMessage
		var subj, body []byte
		if rows.Scan(&m.ID, &m.ParentID, &m.From.ID, &m.From.Name, &m.From.Email, &subj, &body, &m.Date, &m.Draft, &m.Unread, &m.Starred) == nil {
			m.Subject, m.Body = a.mailOpen("subject", m.ID, subj), a.mailOpen("body", m.ID, body)
			m.Mine = m.From.ID == uid
			m.To, m.Cc, m.Bcc, m.Attachments = []mailPerson{}, []mailPerson{}, []mailPerson{}, []mailAttachment{}
			msgs = append(msgs, m)
			ids = append(ids, m.ID)
		}
	}
	rows.Close()
	if len(msgs) == 0 {
		apiError(w, 404, "not found")
		return
	}
	idx := map[int64]*mailMessage{}
	for i := range msgs {
		idx[msgs[i].ID] = &msgs[i]
	}
	if rr, err := a.st.db.Query(ctx, `
		SELECT r.message_id, r.user_id, u.display_name, r.kind FROM mail_recipients r JOIN users u ON u.id=r.user_id JOIN mail_messages m ON m.id=r.message_id
		WHERE r.message_id = ANY($1) AND (r.kind <> 'bcc' OR m.sender_id=$2) ORDER BY u.display_name`, ids, uid); err == nil {
		for rr.Next() {
			var id int64
			var p mailPerson
			var kind string
			if rr.Scan(&id, &p.ID, &p.Name, &kind) == nil {
				m := idx[id]
				switch kind {
				case "to":
					m.To = append(m.To, p)
				case "cc":
					m.Cc = append(m.Cc, p)
				default:
					m.Bcc = append(m.Bcc, p)
				}
			}
		}
		rr.Close()
	}
	if er, err := a.st.db.Query(ctx, `
		SELECT r.message_id, COALESCE(NULLIF(r.name,''), r.address), r.address, r.kind FROM mail_external_recipients r JOIN mail_messages m ON m.id=r.message_id
		WHERE r.message_id = ANY($1) AND (r.kind <> 'bcc' OR m.sender_id=$2) ORDER BY r.address`, ids, uid); err == nil {
		for er.Next() {
			var id int64
			var p mailPerson
			var kind string
			if er.Scan(&id, &p.Name, &p.Email, &kind) == nil {
				m := idx[id]
				switch kind {
				case "to":
					m.To = append(m.To, p)
				case "cc":
					m.Cc = append(m.Cc, p)
				default:
					m.Bcc = append(m.Bcc, p)
				}
			}
		}
		er.Close()
	}
	if ar, err := a.st.db.Query(ctx, `SELECT message_id, id::text, filename, mime, size FROM mail_attachments WHERE message_id = ANY($1) ORDER BY created_at`, ids); err == nil {
		for ar.Next() {
			var id int64
			var at mailAttachment
			if ar.Scan(&id, &at.ID, &at.Filename, &at.Mime, &at.Size) == nil {
				idx[id].Attachments = append(idx[id].Attachments, at)
			}
		}
		ar.Close()
	}
	writeJSON(w, 200, map[string]any{"thread_id": thread, "subject": msgs[len(msgs)-1].Subject, "messages": msgs})
}

// ---------- drafts and sending ----------

type mailDraftReq struct {
	ID            *int64   `json:"id"`
	To            []int64  `json:"to"`
	Cc            []int64  `json:"cc"`
	Bcc           []int64  `json:"bcc"`
	ToExt         []string `json:"to_ext"`
	CcExt         []string `json:"cc_ext"`
	BccExt        []string `json:"bcc_ext"`
	Subject       string   `json:"subject"`
	Body          string   `json:"body"`
	ParentID      *int64   `json:"parent_id"`
	ForwardOf     *int64   `json:"forward_of"`
	AttachmentIDs []string `json:"attachment_ids"`
}

type mailErr struct {
	status int
	msg    string
}

func (e mailErr) Error() string { return e.msg }

func bad(msg string) error { return mailErr{400, msg} }

// visibleMessage reports whether uid has the message in their mailbox (not purged); it returns its thread.
func visibleMessage(ctx context.Context, tx pgx.Tx, uid, id int64) (int64, bool) {
	var thread int64
	err := tx.QueryRow(ctx, `SELECT m.thread_id FROM mail_messages m WHERE m.id=$1 AND EXISTS (SELECT 1 FROM mail_boxes b WHERE b.message_id=m.id AND b.user_id=$2 AND NOT b.purged)`, id, uid).Scan(&thread)
	return thread, err == nil
}

// saveDraft creates or updates a draft owned by uid and returns its id.
func (a *app) saveDraft(ctx context.Context, uid int64, req mailDraftReq) (int64, []mailAttachment, error) {
	subject, body := strings.TrimSpace(req.Subject), req.Body
	if utf8.RuneCountInString(subject) > mailMaxSubject || utf8.RuneCountInString(body) > mailMaxBody {
		return 0, nil, bad("subject (max 300) or body (max 100000) too long")
	}
	// one kind per recipient: to wins over cc, cc over bcc
	kinds := map[int64]string{}
	for _, set := range []struct {
		ids  []int64
		kind string
	}{{req.To, "to"}, {req.Cc, "cc"}, {req.Bcc, "bcc"}} {
		for _, u := range set.ids {
			if _, seen := kinds[u]; !seen {
				kinds[u] = set.kind
			}
		}
	}
	exts, err := a.resolveExternal(ctx, req, kinds)
	if err != nil {
		return 0, nil, err
	}
	if len(exts) > 0 && !mailCfg.outbound() {
		return 0, nil, bad("sending to external addresses is not configured on this server")
	}
	if len(kinds)+len(exts) > mailMaxRecipients {
		return 0, nil, bad("too many recipients (max 50)")
	}
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if len(kinds) > 0 {
		list := make([]int64, 0, len(kinds))
		for u := range kinds {
			list = append(list, u)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE active AND id = ANY($1)`, list).Scan(&n); err != nil || n != len(list) {
			return 0, nil, bad("unknown or inactive recipient")
		}
	}
	var id int64
	if req.ID == nil {
		if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('mail_messages','id'))`).Scan(&id); err != nil {
			return 0, nil, err
		}
		thread := id
		if req.ParentID != nil {
			t, ok := visibleMessage(ctx, tx, uid, *req.ParentID)
			if !ok {
				return 0, nil, mailErr{404, "parent message not found"}
			}
			thread = t
		}
		sj, err1 := a.mailSeal("subject", id, subject)
		bd, err2 := a.mailSeal("body", id, body)
		if err1 != nil || err2 != nil {
			return 0, nil, fmt.Errorf("seal")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_messages(id, thread_id, parent_id, sender_id, subject, body) VALUES ($1,$2,$3,$4,$5,$6)`, id, thread, req.ParentID, uid, sj, bd); err != nil {
			return 0, nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_boxes(message_id, user_id, role, folder, is_read) VALUES ($1,$2,'sender','drafts',TRUE)`, id, uid); err != nil {
			return 0, nil, err
		}
		if req.ForwardOf != nil {
			if _, ok := visibleMessage(ctx, tx, uid, *req.ForwardOf); !ok {
				return 0, nil, mailErr{404, "message to forward not found"}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO mail_attachments(blob_id, message_id, uploader_id, filename, mime, size)
				SELECT blob_id, $1, $2, filename, mime, size FROM mail_attachments WHERE message_id=$3`, id, uid, *req.ForwardOf); err != nil {
				return 0, nil, err
			}
		}
	} else {
		id = *req.ID
		var owner bool
		if tx.QueryRow(ctx, `SELECT true FROM mail_messages m JOIN mail_boxes b ON b.message_id=m.id AND b.user_id=$2 AND b.role='sender' AND b.folder='drafts'
			WHERE m.id=$1 AND m.sender_id=$2 AND m.sent_at IS NULL FOR UPDATE OF m`, id, uid).Scan(&owner) != nil {
			return 0, nil, mailErr{404, "draft not found"}
		}
		sj, err1 := a.mailSeal("subject", id, subject)
		bd, err2 := a.mailSeal("body", id, body)
		if err1 != nil || err2 != nil {
			return 0, nil, fmt.Errorf("seal")
		}
		if _, err := tx.Exec(ctx, `UPDATE mail_messages SET subject=$2, body=$3, updated_at=now() WHERE id=$1`, id, sj, bd); err != nil {
			return 0, nil, err
		}
	}
	// recipients
	if _, err := tx.Exec(ctx, `DELETE FROM mail_recipients WHERE message_id=$1`, id); err != nil {
		return 0, nil, err
	}
	for u, k := range kinds {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_recipients(message_id, user_id, kind) VALUES ($1,$2,$3)`, id, u, k); err != nil {
			return 0, nil, err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mail_external_recipients WHERE message_id=$1`, id); err != nil {
		return 0, nil, err
	}
	for _, e := range exts {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_external_recipients(message_id, address, name, kind) VALUES ($1,$2,NULLIF($3,''),$4)`, id, e.address, e.name, e.kind); err != nil {
			return 0, nil, err
		}
	}
	// attachments: keep the listed ones (this user's uploads, or already on this draft); drop the others.
	// A forwarded message brings its attachments along (copies sharing the same sealed object).
	keep := append([]string{}, req.AttachmentIDs...)
	if req.ID == nil && req.ForwardOf != nil {
		rows, err := tx.Query(ctx, `SELECT id::text FROM mail_attachments WHERE message_id=$1`, id)
		if err != nil {
			return 0, nil, err
		}
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				keep = append(keep, s)
			}
		}
		rows.Close()
	}
	if req.ID != nil || len(keep) > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM mail_attachments WHERE message_id=$1 AND NOT (id::text = ANY($2))`, id, keep); err != nil {
			return 0, nil, err
		}
		for _, aid := range keep {
			tag, err := tx.Exec(ctx, `UPDATE mail_attachments SET message_id=$1 WHERE id::text=$2 AND uploader_id=$3 AND (message_id IS NULL OR message_id=$1)`, id, aid, uid)
			if err != nil || tag.RowsAffected() != 1 {
				return 0, nil, bad("invalid attachment")
			}
		}
	}
	var files int
	var total int64
	if err := tx.QueryRow(ctx, `SELECT COUNT(*), COALESCE(SUM(size),0) FROM mail_attachments WHERE message_id=$1`, id).Scan(&files, &total); err != nil {
		return 0, nil, err
	}
	if files > mailMaxFiles || total > mailMaxTotal {
		return 0, nil, bad("too many attachments (max 10 files, 25 MB in total)")
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	atts := []mailAttachment{}
	if rows, err := a.st.db.Query(ctx, `SELECT id::text, filename, mime, size FROM mail_attachments WHERE message_id=$1 ORDER BY created_at`, id); err == nil {
		for rows.Next() {
			var at mailAttachment
			if rows.Scan(&at.ID, &at.Filename, &at.Mime, &at.Size) == nil {
				atts = append(atts, at)
			}
		}
		rows.Close()
	}
	return id, atts, nil
}

func (a *app) mailFail(w http.ResponseWriter, err error) {
	if me, ok := err.(mailErr); ok {
		apiError(w, me.status, me.msg)
		return
	}
	apiError(w, 500, "internal error")
}

func (a *app) mailSaveDraft(w http.ResponseWriter, r *http.Request) {
	var req mailDraftReq
	if !decodeBody2(w, r, &req) {
		return
	}
	id, atts, err := a.saveDraft(r.Context(), userID(r), req)
	if err != nil {
		a.mailFail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "attachments": atts})
}

func (a *app) mailDiscardDraft(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	// a draft was never sent: it can be removed for good
	tag, err := a.st.db.Exec(ctx, `DELETE FROM mail_messages m WHERE m.id=$1 AND m.sender_id=$2 AND m.sent_at IS NULL`, id, uid)
	if err != nil || tag.RowsAffected() != 1 {
		apiError(w, 404, "draft not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sendDraft turns a draft into a sent message: it appears in the sender's Sent and in each recipient's Inbox.
func (a *app) sendDraft(ctx context.Context, uid int64, id int64) (int64, []int64, error) {
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var thread int64
	if tx.QueryRow(ctx, `SELECT thread_id FROM mail_messages WHERE id=$1 AND sender_id=$2 AND sent_at IS NULL FOR UPDATE`, id, uid).Scan(&thread) != nil {
		return 0, nil, mailErr{404, "draft not found"}
	}
	rows, err := tx.Query(ctx, `SELECT user_id FROM mail_recipients WHERE message_id=$1`, id)
	if err != nil {
		return 0, nil, err
	}
	var rcpt []int64
	for rows.Next() {
		var u int64
		if rows.Scan(&u) == nil {
			rcpt = append(rcpt, u)
		}
	}
	rows.Close()
	var external int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM mail_external_recipients WHERE message_id=$1`, id).Scan(&external); err != nil {
		return 0, nil, err
	}
	if len(rcpt)+external == 0 {
		return 0, nil, bad("add at least one recipient")
	}
	if external > 0 && !mailCfg.outbound() {
		return 0, nil, bad("sending to external addresses is not configured on this server")
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE active AND id = ANY($1)`, rcpt).Scan(&n); err != nil || n != len(rcpt) {
		return 0, nil, bad("unknown or inactive recipient")
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_messages SET sent_at=now(), updated_at=now() WHERE id=$1`, id); err != nil {
		return 0, nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE mail_boxes SET folder='sent' WHERE message_id=$1 AND user_id=$2 AND role='sender'`, id, uid); err != nil {
		return 0, nil, err
	}
	for _, u := range rcpt {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_boxes(message_id, user_id, role, folder, is_read) VALUES ($1,$2,'recipient','inbox',FALSE)`, id, u); err != nil {
			return 0, nil, err
		}
	}
	if external > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO mail_outbox(message_id) VALUES ($1)`, id); err != nil {
			return 0, nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, nil, err
	}
	return thread, rcpt, nil
}

func (a *app) afterSend(ctx context.Context, r *http.Request, uid, id, thread int64, rcpt []int64) {
	_, uname := a.actor(ctx, r)
	a.st.audit(ctx, &uid, uname, "mail_sent", clientIP(r), map[string]any{"message_id": id, "recipients": len(rcpt)})
	var from string
	_ = a.st.db.QueryRow(ctx, `SELECT display_name FROM users WHERE id=$1`, uid).Scan(&from)
	a.hub.publish(ctx, rcpt, "mail", map[string]any{"id": id, "thread_id": thread, "from_id": uid, "from": from})
}

func (a *app) mailSendDraft(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	thread, rcpt, err := a.sendDraft(ctx, uid, id)
	if err != nil {
		a.mailFail(w, err)
		return
	}
	a.afterSend(ctx, r, uid, id, thread, rcpt)
	writeJSON(w, 200, map[string]any{"id": id, "thread_id": thread})
}

// mailSendNow saves (or updates) the draft and sends it in one call.
func (a *app) mailSendNow(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var req mailDraftReq
	if !decodeBody2(w, r, &req) {
		return
	}
	id, _, err := a.saveDraft(ctx, uid, req)
	if err != nil {
		a.mailFail(w, err)
		return
	}
	thread, rcpt, err := a.sendDraft(ctx, uid, id)
	if err != nil {
		// the draft stays: the user can fix the recipients and retry
		a.mailFail(w, err)
		return
	}
	a.afterSend(ctx, r, uid, id, thread, rcpt)
	writeJSON(w, 200, map[string]any{"id": id, "thread_id": thread})
}

// ---------- state changes ----------

// mailBatch applies one action to the caller's own mailbox rows for the given messages.
func (a *app) mailBatch(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var req struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"`
	}
	if !decodeBody2(w, r, &req) || len(req.IDs) == 0 || len(req.IDs) > mailWindow {
		apiError(w, 400, "invalid request")
		return
	}
	var q string
	switch req.Action {
	case "read":
		q = `UPDATE mail_boxes SET is_read=TRUE WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder <> 'drafts'`
	case "unread":
		q = `UPDATE mail_boxes SET is_read=FALSE WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder <> 'drafts'`
	case "star":
		q = `UPDATE mail_boxes SET starred=TRUE WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder <> 'drafts'`
	case "unstar":
		q = `UPDATE mail_boxes SET starred=FALSE WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged`
	case "archive":
		q = `UPDATE mail_boxes SET folder='archive' WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder IN ('inbox','sent')`
	case "trash":
		q = `UPDATE mail_boxes SET trashed_from=folder, folder='trash' WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder IN ('inbox','sent','archive')`
	case "restore":
		q = `UPDATE mail_boxes SET folder=COALESCE(trashed_from, CASE role WHEN 'sender' THEN 'sent' ELSE 'inbox' END), trashed_from=NULL
		     WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder='trash'`
	case "purge":
		q = `UPDATE mail_boxes SET purged=TRUE WHERE user_id=$1 AND message_id = ANY($2) AND NOT purged AND folder='trash'`
	default:
		apiError(w, 400, "unknown action")
		return
	}
	tag, err := a.st.db.Exec(ctx, q, uid, req.IDs)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if req.Action == "purge" {
		_, uname := a.actor(ctx, r)
		a.st.audit(ctx, &uid, uname, "mail_purged", clientIP(r), map[string]any{"count": tag.RowsAffected()})
	}
	writeJSON(w, 200, map[string]any{"updated": tag.RowsAffected()})
}

// ---------- attachments ----------

func (a *app) mailUpload(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "file too large (20 MB max)")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		apiError(w, 400, "file required")
		return
	}
	defer file.Close()
	name := cleanFilename(hdr.Filename)
	if mailBlockedExt[strings.ToLower(path.Ext(name))] {
		apiError(w, http.StatusUnsupportedMediaType, "this file type is not allowed (executables and scripts)")
		return
	}
	plain, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil || int64(len(plain)) > maxUpload {
		apiError(w, http.StatusRequestEntityTooLarge, "file too large (20 MB max)")
		return
	}
	if len(plain) == 0 {
		apiError(w, 400, "empty file")
		return
	}
	detected := http.DetectContentType(plain[:min(512, len(plain))]) // sniffed, never trusted from the client
	var id, blob string
	if err := a.st.db.QueryRow(ctx, `INSERT INTO mail_attachments(blob_id, uploader_id, filename, mime, size) VALUES (gen_random_uuid(), $1,$2,$3,$4) RETURNING id::text, blob_id::text`,
		uid, name, detected, int64(len(plain))).Scan(&id, &blob); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	sealed, err := a.box.Seal(plain, []byte(blob))
	if err == nil {
		_, err = a.s3.PutObject(ctx, attachBucket, "mail/"+blob, bytes.NewReader(sealed), int64(len(sealed)), minio.PutObjectOptions{ContentType: "application/octet-stream"})
	}
	if err != nil {
		_, _ = a.st.db.Exec(ctx, `DELETE FROM mail_attachments WHERE id=$1::uuid`, id)
		apiError(w, 502, "storage unavailable")
		return
	}
	writeJSON(w, 200, mailAttachment{ID: id, Filename: name, Mime: detected, Size: int64(len(plain))})
}

func (a *app) mailDownload(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var blob, name, mt string
	var size int64
	err := a.st.db.QueryRow(ctx, `
		SELECT a.blob_id::text, a.filename, a.mime, a.size FROM mail_attachments a
		WHERE a.id::text=$1 AND ((a.message_id IS NULL AND a.uploader_id=$2)
		   OR EXISTS (SELECT 1 FROM mail_boxes b WHERE b.message_id=a.message_id AND b.user_id=$2 AND NOT b.purged))`, chi.URLParam(r, "id"), uid).Scan(&blob, &name, &mt, &size)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	obj, err := a.s3.GetObject(ctx, attachBucket, "mail/"+blob, minio.GetObjectOptions{})
	if err != nil {
		apiError(w, 502, "storage unavailable")
		return
	}
	defer obj.Close()
	sealed, err := io.ReadAll(io.LimitReader(obj, maxUpload+1024))
	if err != nil {
		apiError(w, 502, "storage unavailable")
		return
	}
	plain, err := a.box.Open(sealed, []byte(blob))
	if err != nil {
		apiError(w, 500, "attachment cannot be decrypted")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream") // never render an attachment in the page
	w.Header().Set("Content-Length", strconv.Itoa(len(plain)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	_, _ = w.Write(plain)
}

// decodeBody2 is decodeBody with a larger limit (mail bodies are big).
func decodeBody2(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 512<<10)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		apiError(w, 400, "invalid request")
		return false
	}
	return true
}
