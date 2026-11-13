package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/minio/minio-go/v7"
)

const recordingsBucket = "recordings"

func (a *app) recordingRoutes(r chi.Router) {
	r.With(a.requireAny("auditor")).Get("/recordings", a.listRecordings)
	r.With(a.requireAny("auditor")).Get("/recordings/{id}/download", a.downloadRecording)
	r.With(a.requireAny("it_admin")).Get("/admin/recording-rules", a.getRecordingRules)
	r.With(a.requireAny("it_admin")).Put("/admin/recording-rules", a.putRecordingRule)
}

// maybeRecord applies the department rules when a call is answered: if the caller or the callee belongs to a
// department that must be recorded, the media service is told before any audio flows. Returns whether the call
// is being recorded (the clients then show a recording indicator).
func (a *app) maybeRecord(ctx context.Context, callID string, caller, callee int64) bool {
	var on bool
	if err := a.st.db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users u JOIN recording_rules r ON r.department_id=u.department_id AND r.enabled
		WHERE u.id = ANY($1))`, []int64{caller, callee}).Scan(&on); err != nil {
		a.hub.log.Error("recording rule lookup failed", "err", err.Error())
		return false
	}
	if !on {
		return false
	}
	base := os.Getenv("MEDIA_URL")
	if base == "" {
		base = "http://media:8080"
	}
	b, _ := json.Marshal(map[string]int64{"caller_user_id": caller, "callee_user_id": callee})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/calls/"+callID+"/record", bytes.NewReader(b))
	if err == nil {
		req.Header.Set("X-Internal-Token", os.Getenv("INTERNAL_TOKEN"))
		req.Header.Set("Content-Type", "application/json")
		c := http.Client{Timeout: 3 * time.Second}
		var res *http.Response
		if res, err = c.Do(req); err == nil {
			res.Body.Close()
			if res.StatusCode != http.StatusNoContent {
				err = fmt.Errorf("media answered %d", res.StatusCode)
			}
		}
	}
	if err != nil {
		// The call goes on (employees must not be cut off), but the failure is on the record.
		a.st.audit(ctx, nil, "system", "recording_failed", "", map[string]any{"call_id": callID, "error": err.Error()})
		return false
	}
	_, _ = a.st.db.Exec(ctx, `UPDATE calls SET recorded=TRUE WHERE id=$1::uuid`, callID)
	return true
}

// internalRecordings is called by the media service once a recording is sealed and stored.
func (a *app) internalRecordings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CallID string `json:"call_id"`
		Tracks []struct {
			Track    string `json:"track"`
			Codec    string `json:"codec"`
			S3Key    string `json:"s3_key"`
			Bytes    int64  `json:"bytes"`
			Duration int    `json:"duration_sec"`
		} `json:"tracks"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req) != nil || req.CallID == "" {
		apiError(w, 400, "invalid request")
		return
	}
	for _, t := range req.Tracks {
		if _, err := a.st.db.Exec(r.Context(), `INSERT INTO call_recordings(call_id, track, codec, s3_key, bytes, duration_sec)
			VALUES ($1::uuid,$2,$3,$4,$5,$6) ON CONFLICT (call_id, track) DO NOTHING`, req.CallID, t.Track, t.Codec, t.S3Key, t.Bytes, t.Duration); err != nil {
			apiError(w, 500, "internal error")
			return
		}
	}
	a.st.audit(r.Context(), nil, "system", "call_recorded", "", map[string]any{"call_id": req.CallID, "tracks": len(req.Tracks)})
	w.WriteHeader(http.StatusNoContent)
}

func (a *app) listRecordings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	q := r.URL.Query()
	from, to := time.Unix(0, 0), time.Now().Add(24*time.Hour)
	if v, err := time.Parse(dateLayout, q.Get("from")); err == nil {
		from = v
	}
	if v, err := time.Parse(dateLayout, q.Get("to")); err == nil {
		to = v.Add(24 * time.Hour)
	}
	userFilter, _ := strconv.ParseInt(q.Get("user_id"), 10, 64)
	rows, err := a.st.db.Query(ctx, `
		SELECT rec.id::text, rec.call_id::text, rec.track, rec.codec, rec.duration_sec, rec.bytes, rec.created_at,
		       c.created_at, cu.display_name, ce.display_name
		FROM call_recordings rec JOIN calls c ON c.id=rec.call_id JOIN users cu ON cu.id=c.caller_id JOIN users ce ON ce.id=c.callee_id
		WHERE c.created_at >= $1 AND c.created_at < $2 AND ($3 = 0 OR c.caller_id = $3 OR c.callee_id = $3)
		ORDER BY c.created_at DESC, rec.track LIMIT 200`, from, to, userFilter)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type row struct {
		ID       string    `json:"id"`
		CallID   string    `json:"call_id"`
		Track    string    `json:"track"`
		Codec    string    `json:"codec"`
		Duration int       `json:"duration_sec"`
		Bytes    int64     `json:"bytes"`
		StoredAt time.Time `json:"stored_at"`
		CallAt   time.Time `json:"call_at"`
		Caller   string    `json:"caller"`
		Callee   string    `json:"callee"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if err := rows.Scan(&x.ID, &x.CallID, &x.Track, &x.Codec, &x.Duration, &x.Bytes, &x.StoredAt, &x.CallAt, &x.Caller, &x.Callee); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, x)
	}
	a.st.audit(ctx, &uid, uname, "recordings_listed", clientIP(r), map[string]any{"user_id": userFilter, "count": len(out)})
	writeJSON(w, 200, map[string]any{"recordings": out})
}

// downloadRecording decrypts a recording for an auditor. Every access is written to the audit journal first.
func (a *app) downloadRecording(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	var callID, track, codec, key string
	if a.st.db.QueryRow(ctx, `SELECT call_id::text, track, codec, s3_key FROM call_recordings WHERE id=$1::uuid`, chi.URLParam(r, "id")).Scan(&callID, &track, &codec, &key) != nil {
		apiError(w, 404, "not found")
		return
	}
	a.st.audit(ctx, &uid, uname, "recording_accessed", clientIP(r), map[string]any{"recording": chi.URLParam(r, "id"), "call_id": callID, "track": track})
	obj, err := a.s3.GetObject(ctx, recordingsBucket, key, minio.GetObjectOptions{})
	if err != nil {
		apiError(w, 502, "storage unavailable")
		return
	}
	defer obj.Close()
	sealed, err := io.ReadAll(io.LimitReader(obj, 512<<20))
	if err != nil {
		apiError(w, 502, "storage unavailable")
		return
	}
	plain, err := a.recBox.Open(sealed, []byte(key))
	if err != nil {
		apiError(w, 500, "recording cannot be decrypted") // wrong key or tampered object
		return
	}
	ext, mime := "wav", "audio/wav"
	if codec == "opus" {
		ext, mime = "ogg", "audio/ogg"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.Itoa(len(plain)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s.%s"`, callID, track, ext))
	_, _ = w.Write(plain)
}

// ---------- department rules ----------

func (a *app) getRecordingRules(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(r.Context(), `SELECT d.id, d.name, COALESCE(rr.enabled, false) FROM departments d LEFT JOIN recording_rules rr ON rr.department_id=d.id ORDER BY d.name`)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	type row struct {
		DepartmentID int    `json:"department_id"`
		Name         string `json:"name"`
		Enabled      bool   `json:"enabled"`
	}
	out := []row{}
	for rows.Next() {
		var x row
		if rows.Scan(&x.DepartmentID, &x.Name, &x.Enabled) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, map[string]any{"rules": out})
}

func (a *app) putRecordingRule(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid, uname := a.actor(ctx, r)
	var req struct {
		DepartmentID int  `json:"department_id"`
		Enabled      bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req) != nil || req.DepartmentID == 0 {
		apiError(w, 400, "invalid request")
		return
	}
	if _, err := a.st.db.Exec(ctx, `INSERT INTO recording_rules(department_id, enabled) VALUES ($1,$2)
		ON CONFLICT (department_id) DO UPDATE SET enabled=EXCLUDED.enabled`, req.DepartmentID, req.Enabled); err != nil {
		apiError(w, 400, "unknown department")
		return
	}
	a.st.audit(ctx, &uid, uname, "recording_rule_changed", clientIP(r), map[string]any{"department_id": req.DepartmentID, "enabled": req.Enabled})
	w.WriteHeader(http.StatusNoContent)
}
