package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

var macRe = regexp.MustCompile(`^[0-9a-f]{12}$`)

func normalizeMAC(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(s)))
}

func (a *app) hasRole(ctx context.Context, uid int64, role string) bool {
	var n int
	_ = a.st.db.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id=$1 AND role=$2`, uid, role).Scan(&n)
	return n > 0
}

func (a *app) requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !a.hasRole(r.Context(), userID(r), role) {
				apiError(w, http.StatusForbidden, "forbidden")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (a *app) adminRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(a.requireRole("it_admin"))
		r.Post("/admin/devices", a.createDevice)
		r.Get("/admin/devices", a.listDevices)
		r.Delete("/admin/devices/{id}", a.deleteDevice)
	})
	r.Put("/me/delegate", a.setDelegate)
}

type deviceRow struct {
	ID         int       `json:"id"`
	UserID     int64     `json:"user_id"`
	User       string    `json:"user"`
	Extension  string    `json:"extension"`
	Type       string    `json:"type"`
	Vendor     string    `json:"vendor"`
	Model      string    `json:"model"`
	MAC        string    `json:"mac,omitempty"`
	SIPUser    string    `json:"sip_username"`
	Active     bool      `json:"active"`
	Registered bool      `json:"registered"`
	CreatedAt  time.Time `json:"created_at"`
}

// createDevice generates the SIP account for a phone. The password is returned once; it is stored sealed
// (the provisioning service needs it to render the phone's configuration file).
func (a *app) createDevice(w http.ResponseWriter, r *http.Request) {
	ctx, admin := r.Context(), userID(r)
	var req struct {
		UserID int64  `json:"user_id"`
		Type   string `json:"type"`
		Vendor string `json:"vendor"`
		Model  string `json:"model"`
		MAC    string `json:"mac"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req) != nil || req.UserID == 0 || (req.Type != "desk_phone" && req.Type != "softphone") {
		apiError(w, 400, "invalid request")
		return
	}
	if req.Vendor == "" {
		req.Vendor = "generic"
	}
	mac := normalizeMAC(req.MAC)
	if mac != "" && !macRe.MatchString(mac) {
		apiError(w, 400, "invalid MAC address")
		return
	}
	if req.Type == "desk_phone" && mac == "" {
		apiError(w, 400, "a desk phone needs its MAC address (used by auto-provisioning)")
		return
	}
	u, err := a.st.getUser(ctx, req.UserID)
	if err != nil || u.Extension == "" {
		apiError(w, 400, "user not found or without extension")
		return
	}
	var existing int
	_ = a.st.db.QueryRow(ctx, `SELECT count(*) FROM devices WHERE user_id=$1`, u.ID).Scan(&existing)
	sipUser := u.Extension
	if existing > 0 {
		sipUser = fmt.Sprintf("%s-%d", u.Extension, existing+1)
	}
	raw := make([]byte, 15)
	_, _ = rand.Read(raw)
	password := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	sealed, err := a.sipBox.Seal([]byte(password), []byte(sipUser))
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	var id int
	var macArg any
	if mac != "" {
		macArg = mac
	}
	if err := a.st.db.QueryRow(ctx, `INSERT INTO devices(user_id, type, vendor, model, mac, sip_username, sip_secret_enc)
		VALUES ($1,$2,$3,NULLIF($4,''),$5,$6,$7) RETURNING id`, u.ID, req.Type, req.Vendor, req.Model, macArg, sipUser, sealed).Scan(&id); err != nil {
		apiError(w, 409, "device already exists (duplicate MAC or username)")
		return
	}
	adm, _ := a.st.getUser(ctx, admin)
	a.st.audit(ctx, &admin, adm.Username, "device_created", clientIP(r), map[string]any{"device_id": id, "user_id": u.ID, "type": req.Type, "mac": mac})
	out := map[string]any{"id": id, "user_id": u.ID, "extension": u.Extension, "sip_username": sipUser, "sip_password": password, "sip_domain": os.Getenv("SIP_DOMAIN")}
	if mac != "" {
		out["provisioning_path"] = "/prov/" + mac + ".cfg"
	}
	writeJSON(w, 200, out)
}

func (a *app) listDevices(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.db.Query(r.Context(), `
		SELECT d.id, d.user_id, u.display_name, COALESCE(u.extension,''), d.type, d.vendor, COALESCE(d.model,''), COALESCE(d.mac,''),
		       d.sip_username, d.active, COALESCE(sr.expires_at > now(), false), d.created_at
		FROM devices d JOIN users u ON u.id=d.user_id LEFT JOIN sip_registrations sr ON sr.device_id=d.id ORDER BY d.id`)
	if err != nil {
		apiError(w, 500, "internal error")
		return
	}
	defer rows.Close()
	out := []deviceRow{}
	for rows.Next() {
		var d deviceRow
		if err := rows.Scan(&d.ID, &d.UserID, &d.User, &d.Extension, &d.Type, &d.Vendor, &d.Model, &d.MAC, &d.SIPUser, &d.Active, &d.Registered, &d.CreatedAt); err != nil {
			apiError(w, 500, "internal error")
			return
		}
		out = append(out, d)
	}
	writeJSON(w, 200, map[string]any{"devices": out})
}

func (a *app) deleteDevice(w http.ResponseWriter, r *http.Request) {
	ctx, admin := r.Context(), userID(r)
	id, ok := pathID(r, "id")
	if !ok {
		apiError(w, 400, "invalid id")
		return
	}
	var owner int64
	if a.st.db.QueryRow(ctx, `UPDATE devices SET active=FALSE WHERE id=$1 AND active RETURNING user_id`, id).Scan(&owner) != nil {
		apiError(w, 404, "not found")
		return
	}
	a.rdb.HDel(ctx, "sip:reg:"+strconv.FormatInt(owner, 10), strconv.FormatInt(id, 10))
	adm, _ := a.st.getUser(ctx, admin)
	a.st.audit(ctx, &admin, adm.Username, "device_disabled", clientIP(r), map[string]any{"device_id": id})
	w.WriteHeader(http.StatusNoContent)
}

// setDelegate chooses who receives calls while the user's status is "away" (null clears it).
func (a *app) setDelegate(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), userID(r)
	var req struct {
		UserID *int64 `json:"user_id"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&req) != nil {
		apiError(w, 400, "invalid request")
		return
	}
	if req.UserID != nil {
		if d, err := a.st.getUser(ctx, *req.UserID); err != nil || !d.Active || d.ID == uid {
			apiError(w, 400, "invalid delegate")
			return
		}
	}
	if _, err := a.st.db.Exec(ctx, `UPDATE users SET delegate_user_id=$2 WHERE id=$1`, uid, req.UserID); err != nil {
		apiError(w, 500, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
