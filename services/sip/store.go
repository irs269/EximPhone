package main

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/irs269/eximphone/services/pkg/crypto"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type device struct {
	ID        int
	UserID    int64
	Extension string
	Password  string // decrypted in memory only
}

// binding is a live registration: where to reach one device right now.
type binding struct {
	DeviceID  int    `json:"device_id"`
	Extension string `json:"extension"`
	Source    string `json:"source"` // observed ip:port (NAT-friendly: we never trust the Contact address)
	Transport string `json:"transport"`
	Expires   int64  `json:"expires"` // unix seconds
}

type store struct {
	db  *pgxpool.Pool
	rdb *redis.Client
	box *crypto.Box
}

func regKey(userID int64) string { return "sip:reg:" + strconv.FormatInt(userID, 10) }

// deviceBySIPUser loads an active device and decrypts its secret (digest needs the clear password).
func (s *store) deviceBySIPUser(ctx context.Context, sipUser string) (*device, error) {
	var d device
	var sealed []byte
	err := s.db.QueryRow(ctx, `SELECT d.id, d.user_id, COALESCE(u.extension,''), d.sip_secret_enc
		FROM devices d JOIN users u ON u.id=d.user_id WHERE d.sip_username=$1 AND d.active AND u.active`, sipUser).
		Scan(&d.ID, &d.UserID, &d.Extension, &sealed)
	if err != nil {
		return nil, err
	}
	plain, err := s.box.Open(sealed, []byte(sipUser))
	if err != nil {
		return nil, err
	}
	d.Password = string(plain)
	return &d, nil
}

func (s *store) bind(ctx context.Context, d *device, source, transport, contact, ua string, ttl time.Duration) error {
	exp := time.Now().Add(ttl)
	b, _ := json.Marshal(binding{DeviceID: d.ID, Extension: d.Extension, Source: source, Transport: transport, Expires: exp.Unix()})
	key := regKey(d.UserID)
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, key, strconv.Itoa(d.ID), b)
	pipe.Expire(ctx, key, 2*time.Hour)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	_, err := s.db.Exec(ctx, `INSERT INTO sip_registrations(device_id, source_addr, contact, user_agent, expires_at)
		VALUES ($1,$2,$3,NULLIF($4,''),$5)
		ON CONFLICT (device_id) DO UPDATE SET source_addr=EXCLUDED.source_addr, contact=EXCLUDED.contact,
		  user_agent=EXCLUDED.user_agent, expires_at=EXCLUDED.expires_at, updated_at=now()`, d.ID, source, contact, ua, exp)
	return err
}

func (s *store) unbind(ctx context.Context, d *device) {
	s.rdb.HDel(ctx, regKey(d.UserID), strconv.Itoa(d.ID))
	_, _ = s.db.Exec(ctx, `UPDATE sip_registrations SET expires_at=now() WHERE device_id=$1`, d.ID)
}

// contacts returns the live (non-expired) registrations of a user.
func (s *store) contacts(ctx context.Context, userID int64) []binding {
	m, err := s.rdb.HGetAll(ctx, regKey(userID)).Result()
	if err != nil {
		return nil
	}
	var out []binding
	for field, raw := range m {
		var b binding
		if json.Unmarshal([]byte(raw), &b) != nil || b.Expires < time.Now().Unix() {
			s.rdb.HDel(ctx, regKey(userID), field)
			continue
		}
		out = append(out, b)
	}
	return out
}

func (s *store) userByExtension(ctx context.Context, ext string) (int64, bool) {
	var id int64
	err := s.db.QueryRow(ctx, `SELECT id FROM users WHERE extension=$1 AND active`, ext).Scan(&id)
	return id, err != pgx.ErrNoRows && err == nil
}
