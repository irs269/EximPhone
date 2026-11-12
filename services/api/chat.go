package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/minio/minio-go/v7"
)

const (
	maxBody       = 4000
	maxUpload     = 20 << 20
	attachBucket  = "attachments"
	searchWindow  = 1000
	searchResults = 30
)

var allowedMIME = map[string]bool{"application/pdf": true, "image/png": true, "image/jpeg": true}

var errNotMember = errors.New("not a member")

type attachmentJSON struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
}

type messageJSON struct {
	ID             int64           `json:"id"`
	ConversationID int64           `json:"conversation_id"`
	SenderID       int64           `json:"sender_id"`
	Body           string          `json:"body"`
	CreatedAt      time.Time       `json:"created_at"`
	Deleted        bool            `json:"deleted,omitempty"`
	Status         string          `json:"status,omitempty"` // own messages: sent | delivered | read
	Attachment     *attachmentJSON `json:"attachment,omitempty"`
}

func (a *app) aad(convID int64) []byte { return []byte(strconv.FormatInt(convID, 10)) }

// members returns the other members of a conversation, or errNotMember if uid is not in it.
func (a *app) members(ctx context.Context, convID, uid int64) ([]int64, error) {
	rows, err := a.st.db.Query(ctx, `SELECT user_id FROM conversation_members WHERE conversation_id=$1`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []int64
	isMember := false
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		all = append(all, id)
		if id == uid {
			isMember = true
		}
	}
	if !isMember {
		return nil, errNotMember
	}
	return all, rows.Err()
}

func pathID(r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	return id, err == nil
}

func (a *app) chatRoutes(r chi.Router) {
	r.Get("/users", a.listUsers)
	r.Get("/presence", a.getPresence)
	r.Put("/presence", a.putPresence)
	r.Get("/conversations", a.listConversations)
	r.Post("/conversations", a.createConversation)
	r.Get("/conversations/{id}/messages", a.listMessages)
	r.Post("/conversations/{id}/messages", a.sendMessage)
	r.Post("/conversations/{id}/read", a.markRead)
	r.Delete("/messages/{id}", a.deleteMessage)
	r.Get("/search", a.search)
	r.Post("/attachments", a.upload)
	r.Get("/attachments/{id}", a.download)
}

// ---------- directory & presence ----------

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func (a *app) listUsers(w http.ResponseWriter, r *http.Request) {
	q := "%" + likeEscaper.Replace(strings.TrimSpace(r.URL.Query().Get("q"))) + "%"
	rows, err := a.st.db.Query(r.Context(), `
		SELECT u.id, u.username::text, u.display_name, COALESCE(u.extension,''), COALESCE(d.name,''), COALESCE(s.name,''), COALESCE(u.email::text,'')
		FROM users u LEFT JOIN departments d ON d.id=u.department_id LEFT JOIN sites s ON s.id=u.site_id
		WHERE u.active AND (u.display_name ILIKE $1 OR u.extension ILIKE $1 OR d.name ILIKE $1)
		ORDER BY u.display_name LIMIT 200`, q)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type userRow struct {
		ID         int64  `json:"id"`
		Username   string `json:"username"`
		Name       string `json:"display_name"`
		Extension  string `json:"extension"`
		Department string `json:"department"`
		Site       string `json:"site"`
		Email      string `json:"email"`
	}
	out := []userRow{}
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.Extension, &u.Department, &u.Site, &u.Email); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, u)
	}
	writeJSON(w, 200, map[string]any{"users": out})
}

var settablePresence = map[string]bool{"on": true, "busy": true, "meet": true, "away": true, "dnd": true}

// setPresence stores the status and broadcasts it; "offline" removes the entry.
func (a *app) setPresence(ctx context.Context, uid int64, status string) {
	if status == "offline" {
		a.rdb.HDel(ctx, "presence", strconv.FormatInt(uid, 10))
	} else {
		a.rdb.HSet(ctx, "presence", strconv.FormatInt(uid, 10), status)
	}
	a.hub.publish(ctx, nil, "presence", map[string]any{"user_id": uid, "status": status})
}

func (a *app) getPresence(w http.ResponseWriter, r *http.Request) {
	m, err := a.rdb.HGetAll(r.Context(), "presence").Result()
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	writeJSON(w, 200, map[string]any{"presence": m})
}

func (a *app) putPresence(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req) != nil || !settablePresence[req.Status] {
		apiError(w, 400, "invalid status")
		return
	}
	a.setPresence(r.Context(), userID(r), req.Status)
	writeJSON(w, 200, map[string]string{"status": req.Status})
}

// ---------- conversations ----------

type conversationJSON struct {
	ID          int64        `json:"id"`
	Kind        string       `json:"kind"`
	Name        string       `json:"name,omitempty"`
	Members     []int64      `json:"members"`
	Unread      int          `json:"unread"`
	LastMessage *messageJSON `json:"last_message,omitempty"`
}

func (a *app) conversationFor(ctx context.Context, convID, uid int64) (*conversationJSON, error) {
	c := conversationJSON{ID: convID}
	var lastID *int64
	err := a.st.db.QueryRow(ctx, `
		SELECT c.kind, COALESCE(c.name,''),
		  (SELECT count(*) FROM messages m WHERE m.conversation_id=c.id AND m.id>cm.last_read_id AND m.sender_id<>$2 AND m.deleted_at IS NULL),
		  (SELECT max(id) FROM messages WHERE conversation_id=c.id)
		FROM conversations c JOIN conversation_members cm ON cm.conversation_id=c.id AND cm.user_id=$2
		WHERE c.id=$1`, convID, uid).Scan(&c.Kind, &c.Name, &c.Unread, &lastID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errNotMember
	}
	if err != nil {
		return nil, err
	}
	if c.Members, err = a.members(ctx, convID, uid); err != nil {
		return nil, err
	}
	if lastID != nil {
		msgs, err := a.fetchMessages(ctx, convID, uid, 0, 1)
		if err == nil && len(msgs) == 1 {
			c.LastMessage = &msgs[0]
		}
	}
	return &c, nil
}

func (a *app) listConversations(w http.ResponseWriter, r *http.Request) {
	uid := userID(r)
	rows, err := a.st.db.Query(r.Context(), `
		SELECT c.id FROM conversations c JOIN conversation_members cm ON cm.conversation_id=c.id AND cm.user_id=$1
		ORDER BY COALESCE((SELECT max(id) FROM messages WHERE conversation_id=c.id),0) DESC, c.id DESC`, uid)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	out := []conversationJSON{}
	for _, id := range ids {
		if c, err := a.conversationFor(r.Context(), id, uid); err == nil {
			out = append(out, *c)
		}
	}
	writeJSON(w, 200, map[string]any{"conversations": out})
}

func (a *app) createConversation(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var req struct {
		MemberIDs []int64 `json:"member_ids"`
		Name      string  `json:"name"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	set := map[int64]bool{}
	for _, id := range req.MemberIDs {
		if id != uid {
			set[id] = true
		}
	}
	if len(set) == 0 || len(set) > 200 {
		apiError(w, 400, "choose between 1 and 200 other members")
		return
	}
	others := make([]int64, 0, len(set))
	for id := range set {
		others = append(others, id)
	}
	var found int
	if err := a.st.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE active AND id = ANY($1)`, others).Scan(&found); err != nil || found != len(others) {
		apiError(w, 400, "unknown member")
		return
	}

	var convID int64
	name := strings.TrimSpace(req.Name)
	if len(others) == 1 && name == "" {
		lo, hi := uid, others[0]
		if lo > hi {
			lo, hi = hi, lo
		}
		key := fmt.Sprintf("%d:%d", lo, hi)
		err := a.st.db.QueryRow(ctx, `SELECT id FROM conversations WHERE direct_key=$1`, key).Scan(&convID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = a.st.db.QueryRow(ctx, `INSERT INTO conversations(kind, direct_key, created_by) VALUES ('direct',$1,$2)
				ON CONFLICT (direct_key) DO UPDATE SET direct_key=EXCLUDED.direct_key RETURNING id`, key, uid).Scan(&convID)
		}
		if err != nil {
			apiError(w, 500, "internal error")
			return
		}
	} else {
		if name == "" || utf8.RuneCountInString(name) > 80 {
			apiError(w, 400, "a group needs a name (max 80 characters)")
			return
		}
		if err := a.st.db.QueryRow(ctx, `INSERT INTO conversations(kind, name, created_by) VALUES ('group',$1,$2) RETURNING id`, name, uid).Scan(&convID); err != nil {
			apiError(w, 500, "internal error")
			return
		}
	}
	_, err := a.st.db.Exec(ctx, `INSERT INTO conversation_members(conversation_id, user_id)
		SELECT $1, x FROM unnest($2::bigint[]) x ON CONFLICT DO NOTHING`, convID, append(others, uid))
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	c, err := a.conversationFor(ctx, convID, uid)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	a.hub.publish(ctx, c.Members, "conversation", map[string]int64{"conversation_id": convID})
	writeJSON(w, 200, c)
}

// ---------- messages ----------

func (a *app) fetchMessages(ctx context.Context, convID, uid, before int64, limit int) ([]messageJSON, error) {
	rows, err := a.st.db.Query(ctx, `
		SELECT m.id, m.sender_id, m.body, m.created_at, m.deleted_at IS NOT NULL,
		  a.id::text, a.filename, a.mime, a.size,
		  CASE WHEN m.sender_id=$2 THEN COALESCE((SELECT CASE WHEN bool_and(r.read_at IS NOT NULL) THEN 'read'
		        WHEN bool_and(r.delivered_at IS NOT NULL) THEN 'delivered' ELSE 'sent' END
		        FROM message_receipts r WHERE r.message_id=m.id), 'sent') END
		FROM messages m LEFT JOIN attachments a ON a.id=m.attachment_id
		WHERE m.conversation_id=$1 AND ($3=0 OR m.id<$3) ORDER BY m.id DESC LIMIT $4`, convID, uid, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []messageJSON
	for rows.Next() {
		m := messageJSON{ConversationID: convID}
		var sealed []byte
		var attID, attName, attMime *string
		var attSize *int64
		var status *string
		if err := rows.Scan(&m.ID, &m.SenderID, &sealed, &m.CreatedAt, &m.Deleted, &attID, &attName, &attMime, &attSize, &status); err != nil {
			return nil, err
		}
		if status != nil {
			m.Status = *status
		}
		if !m.Deleted {
			plain, err := a.box.Open(sealed, a.aad(convID))
			if err != nil {
				return nil, err
			}
			m.Body = string(plain)
			if attID != nil {
				m.Attachment = &attachmentJSON{ID: *attID, Filename: *attName, Mime: *attMime, Size: *attSize}
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (a *app) listMessages(w http.ResponseWriter, r *http.Request) {
	convID, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	uid := userID(r)
	if _, err := a.members(r.Context(), convID, uid); err != nil {
		apiError(w, 404, "not found")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	msgs, err := a.fetchMessages(r.Context(), convID, uid, before, limit)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	if msgs == nil {
		msgs = []messageJSON{}
	}
	writeJSON(w, 200, map[string]any{"messages": msgs, "has_more": len(msgs) == limit})
}

func (a *app) sendMessage(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	convID, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	var req struct {
		Body         string `json:"body"`
		AttachmentID string `json:"attachment_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	body := strings.TrimSpace(req.Body)
	if (body == "" && req.AttachmentID == "") || utf8.RuneCountInString(body) > maxBody {
		apiError(w, 400, "message must be 1 to 4000 characters")
		return
	}
	members, err := a.members(ctx, convID, uid)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	sealed, err := a.box.Seal([]byte(body), a.aad(convID))
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var att *string
	if req.AttachmentID != "" {
		att = &req.AttachmentID
	}
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer tx.Rollback(ctx)
	var msgID int64
	var created time.Time
	if att != nil {
		// The attachment must be this user's upload in this conversation and not yet used.
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM attachments a WHERE a.id=$1::uuid AND a.conversation_id=$2 AND a.uploader_id=$3
			AND NOT EXISTS (SELECT 1 FROM messages m WHERE m.attachment_id=a.id)`, *att, convID, uid).Scan(&n); err != nil || n != 1 {
			apiError(w, 400, "invalid attachment")
			return
		}
	}
	if err := tx.QueryRow(ctx, `INSERT INTO messages(conversation_id, sender_id, body, attachment_id) VALUES ($1,$2,$3,$4::uuid)
		RETURNING id, created_at`, convID, uid, sealed, att).Scan(&msgID, &created); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `INSERT INTO message_receipts(message_id, user_id)
		SELECT $1, user_id FROM conversation_members WHERE conversation_id=$2 AND user_id<>$3`, msgID, convID, uid); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE conversation_members SET last_read_id=$1 WHERE conversation_id=$2 AND user_id=$3`, msgID, convID, uid); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	msgs, err := a.fetchMessages(ctx, convID, uid, msgID+1, 1)
	if err != nil || len(msgs) == 0 {
		apiError(w, 500, "internal error")
		return
	}
	a.hub.publish(ctx, members, "message", msgs[0])
	writeJSON(w, 200, msgs[0])
}

// markDelivered is called when a recipient's client confirms it received a message over the socket.
func (a *app) markDelivered(ctx context.Context, uid, msgID int64) {
	var sender, conv int64
	err := a.st.db.QueryRow(ctx, `UPDATE message_receipts r SET delivered_at=now() FROM messages m
		WHERE r.message_id=m.id AND r.message_id=$1 AND r.user_id=$2 AND r.delivered_at IS NULL
		RETURNING m.sender_id, m.conversation_id`, msgID, uid).Scan(&sender, &conv)
	if err == nil {
		a.hub.publish(ctx, []int64{sender}, "receipt", map[string]any{"conversation_id": conv, "message_ids": []int64{msgID}, "user_id": uid, "state": "delivered"})
	}
}

func (a *app) markRead(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	convID, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	if _, err := a.members(ctx, convID, uid); err != nil {
		apiError(w, 404, "not found")
		return
	}
	if _, err := a.st.db.Exec(ctx, `UPDATE conversation_members SET last_read_id=COALESCE((SELECT max(id) FROM messages WHERE conversation_id=$1),0)
		WHERE conversation_id=$1 AND user_id=$2`, convID, uid); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	rows, err := a.st.db.Query(ctx, `UPDATE message_receipts r SET read_at=now(), delivered_at=COALESCE(r.delivered_at, now()) FROM messages m
		WHERE r.message_id=m.id AND m.conversation_id=$1 AND r.user_id=$2 AND r.read_at IS NULL RETURNING m.id, m.sender_id`, convID, uid)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	bySender := map[int64][]int64{}
	for rows.Next() {
		var id, sender int64
		if rows.Scan(&id, &sender) == nil {
			bySender[sender] = append(bySender[sender], id)
		}
	}
	rows.Close()
	for sender, ids := range bySender {
		a.hub.publish(ctx, []int64{sender}, "receipt", map[string]any{"conversation_id": convID, "message_ids": ids, "user_id": uid, "state": "read"})
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteMessage is a logical deletion: the sealed row is kept for the regulatory archive.
func (a *app) deleteMessage(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	id, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	var conv int64
	err := a.st.db.QueryRow(ctx, `UPDATE messages SET deleted_at=now(), deleted_by=$2 WHERE id=$1 AND sender_id=$2 AND deleted_at IS NULL RETURNING conversation_id`, id, uid).Scan(&conv)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	u, _ := a.st.getUser(ctx, uid)
	a.st.audit(ctx, &uid, u.Username, "message_deleted", clientIP(r), map[string]any{"message_id": id, "conversation_id": conv})
	if members, err := a.members(ctx, conv, uid); err == nil {
		a.hub.publish(ctx, members, "message_deleted", map[string]int64{"conversation_id": conv, "message_id": id})
	}
	w.WriteHeader(http.StatusNoContent)
}

// search scans the user's most recent messages: bodies are encrypted at rest, so SQL cannot match them.
func (a *app) search(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	if utf8.RuneCountInString(q) < 2 {
		writeJSON(w, 200, map[string]any{"results": []messageJSON{}})
		return
	}
	rows, err := a.st.db.Query(ctx, `
		SELECT m.id, m.conversation_id, m.sender_id, m.body, m.created_at FROM messages m
		JOIN conversation_members cm ON cm.conversation_id=m.conversation_id AND cm.user_id=$1
		WHERE m.deleted_at IS NULL ORDER BY m.id DESC LIMIT $2`, uid, searchWindow)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []messageJSON{}
	for rows.Next() && len(out) < searchResults {
		var m messageJSON
		var sealed []byte
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.SenderID, &sealed, &m.CreatedAt); err != nil {
			break
		}
		if plain, err := a.box.Open(sealed, a.aad(m.ConversationID)); err == nil && strings.Contains(strings.ToLower(string(plain)), q) {
			m.Body = string(plain)
			out = append(out, m)
		}
	}
	writeJSON(w, 200, map[string]any{"results": out})
}

// ---------- attachments ----------

func cleanFilename(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '/' {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || len(name) > 150 {
		return "file"
	}
	return name
}

func (a *app) upload(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		apiError(w, http.StatusRequestEntityTooLarge, "file too large (20 MB max)")
		return
	}
	convID, err := strconv.ParseInt(r.FormValue("conversation_id"), 10, 64)
	if err != nil {
		apiError(w, 400, "conversation_id required")
		return
	}
	if _, err := a.members(ctx, convID, uid); err != nil {
		apiError(w, 404, "not found")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		apiError(w, 400, "file required")
		return
	}
	defer file.Close()
	if hdr.Size > maxUpload {
		apiError(w, http.StatusRequestEntityTooLarge, "file too large (20 MB max)")
		return
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	detected := http.DetectContentType(head[:n]) // content sniffing, never trust the client header
	if !allowedMIME[detected] {
		apiError(w, http.StatusUnsupportedMediaType, "only PDF, PNG and JPEG files are allowed")
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var id string
	name := cleanFilename(hdr.Filename)
	if err := a.st.db.QueryRow(ctx, `INSERT INTO attachments(conversation_id, uploader_id, filename, mime, size, s3_key)
		VALUES ($1,$2,$3,$4,$5,'') RETURNING id::text`, convID, uid, name, detected, hdr.Size).Scan(&id); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	key := fmt.Sprintf("conversation-%d/%s", convID, id)
	if _, err := a.s3.PutObject(ctx, attachBucket, key, file, hdr.Size, minio.PutObjectOptions{ContentType: detected}); err != nil {
		apiError(w, 502, "storage unavailable")
		return
	}
	_, _ = a.st.db.Exec(ctx, `UPDATE attachments SET s3_key=$2 WHERE id=$1::uuid`, id, key)
	writeJSON(w, 200, attachmentJSON{ID: id, Filename: name, Mime: detected, Size: hdr.Size})
}

func (a *app) download(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var conv, size int64
	var name, mt, key string
	err := a.st.db.QueryRow(ctx, `SELECT conversation_id, filename, mime, size, s3_key FROM attachments WHERE id=$1::uuid`, chi.URLParam(r, "id")).
		Scan(&conv, &name, &mt, &size, &key)
	if err != nil {
		apiError(w, 404, "not found")
		return
	}
	if _, err := a.members(ctx, conv, uid); err != nil {
		apiError(w, 404, "not found")
		return
	}
	obj, err := a.s3.GetObject(ctx, attachBucket, key, minio.GetObjectOptions{})
	if err != nil {
		apiError(w, 502, "storage unavailable")
		return
	}
	defer obj.Close()
	w.Header().Set("Content-Type", mt)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	_, _ = io.Copy(w, obj)
}
