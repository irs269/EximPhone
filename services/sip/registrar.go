package main

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/irs269/eximphone/services/pkg/sipauth"

	"github.com/emiago/sipgo/sip"
)

const (
	maxFailures    = 20 // failed authentications per source IP per window
	failureWindow  = 10 * time.Minute
	maxRegistryTTL = time.Hour
)

func (s *service) reply(req *sip.Request, tx sip.ServerTransaction, code sip.StatusCode, reason string) {
	_ = tx.Respond(sip.NewResponseFromRequest(req, code, reason, nil))
}

func (s *service) challenge(req *sip.Request, tx sip.ServerTransaction, stale bool) {
	res := sip.NewResponseFromRequest(req, sip.StatusUnauthorized, "Unauthorized", nil)
	for _, c := range sipauth.Challenges(s.cfg.domain, s.cfg.nonceSecret, time.Now(), stale) {
		res.AppendHeader(sip.NewHeader("WWW-Authenticate", c))
	}
	_ = tx.Respond(res)
}

func sourceIP(req *sip.Request) string {
	host, _, err := net.SplitHostPort(req.Source())
	if err != nil {
		return req.Source()
	}
	return host
}

func (s *service) failed(ctx context.Context, ip string) {
	k := "sip:fail:" + ip
	if s.st.rdb.Incr(ctx, k).Val() == 1 {
		s.st.rdb.Expire(ctx, k, failureWindow)
	}
}

// authenticate performs digest authentication (SHA-256 or MD5) and returns the device that sent the request.
// On failure the response has already been sent.
func (s *service) authenticate(req *sip.Request, tx sip.ServerTransaction) (*device, bool) {
	ctx := context.Background()
	ip := sourceIP(req)
	if n, _ := s.st.rdb.Get(ctx, "sip:fail:"+ip).Int(); n >= maxFailures {
		s.reply(req, tx, sip.StatusForbidden, "Forbidden") // too many failures from this address
		return nil, false
	}
	h := req.GetHeader("Authorization")
	if h == nil {
		s.challenge(req, tx, false)
		return nil, false
	}
	params, err := sipauth.Parse(h.Value())
	if err != nil {
		s.reply(req, tx, sip.StatusBadRequest, "Bad Request")
		return nil, false
	}
	now := time.Now()
	if !sipauth.NonceValid(s.cfg.nonceSecret, params["nonce"], now) {
		s.challenge(req, tx, true) // expired nonce: ask the phone to retry
		return nil, false
	}
	dev, err := s.st.deviceBySIPUser(ctx, params["username"])
	if err != nil || !sipauth.Verify(params, string(req.Method), dev.Password, s.cfg.domain, s.cfg.nonceSecret, now) {
		s.failed(ctx, ip)
		s.reply(req, tx, sip.StatusForbidden, "Forbidden")
		return nil, false
	}
	// The account may only act as the extension of its owner.
	if from := req.From(); from == nil || from.Address.User != dev.Extension {
		s.failed(ctx, ip)
		s.reply(req, tx, sip.StatusForbidden, "Forbidden")
		return nil, false
	}
	return dev, true
}

func (s *service) onRegister(req *sip.Request, tx sip.ServerTransaction) {
	dev, ok := s.authenticate(req, tx)
	if !ok {
		return
	}
	ctx := context.Background()

	expires := 3600
	if h := req.GetHeader("Expires"); h != nil {
		if v, err := strconv.Atoi(strings.TrimSpace(h.Value())); err == nil {
			expires = v
		}
	}
	contact := req.Contact()
	if contact != nil {
		if v, ok := contact.Params.Get("expires"); ok {
			if n, err := strconv.Atoi(v); err == nil {
				expires = n
			}
		}
	}
	if expires > int(maxRegistryTTL.Seconds()) {
		expires = int(maxRegistryTTL.Seconds())
	}

	if expires == 0 {
		s.st.unbind(ctx, dev)
		s.log.Info("unregistered", "ext", dev.Extension, "device", dev.ID)
	} else {
		ua := ""
		if h := req.GetHeader("User-Agent"); h != nil {
			ua = h.Value()
		}
		cs := ""
		if contact != nil {
			cs = contact.Address.String()
		}
		if err := s.st.bind(ctx, dev, req.Source(), strings.ToLower(req.Transport()), cs, ua, time.Duration(expires)*time.Second); err != nil {
			s.log.Error("bind failed", "err", err)
			s.reply(req, tx, sip.StatusServiceUnavailable, "Service Unavailable")
			return
		}
		s.log.Info("registered", "ext", dev.Extension, "device", dev.ID, "source", req.Source(), "expires", expires)
	}

	res := sip.NewResponseFromRequest(req, sip.StatusOK, "OK", nil)
	if contact != nil {
		c := contact.Clone()
		c.Params.Add("expires", strconv.Itoa(expires))
		res.AppendHeader(c)
	}
	res.AppendHeader(sip.NewHeader("Date", time.Now().UTC().Format(time.RFC1123)))
	_ = tx.Respond(res)
}
