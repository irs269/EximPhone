package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // decode ISO-8859-x, windows-125x, ... in received mail
	gomail "github.com/emersion/go-message/mail"
	"github.com/minio/minio-go/v7"
)

// External mail through the bank's existing mail server.
//
//	outgoing: messages with external recipients are queued (mail_outbox) and handed to an SMTP relay.
//	incoming: a mailbox is polled over IMAP; each unread message is filed in the inbox of the platform users it is addressed to.
//
// Everything is optional and configured by environment variables (see docs/mail.md):
//
//	MAIL_SMTP_HOST MAIL_SMTP_PORT MAIL_SMTP_SECURITY(starttls|tls|none) MAIL_SMTP_USER MAIL_SMTP_PASSWORD
//	MAIL_FROM_MODE(user|fixed) MAIL_FROM_ADDRESS MAIL_DOMAIN MAIL_CA_FILE
//	MAIL_IMAP_HOST MAIL_IMAP_PORT MAIL_IMAP_SECURITY(tls|starttls|none) MAIL_IMAP_USER MAIL_IMAP_PASSWORD MAIL_IMAP_FOLDER MAIL_POLL_SECONDS

type mailExtConfig struct {
	smtpHost, smtpSecurity, smtpUser, smtpPass string
	smtpPort                                   int
	fromMode, fromAddress, domain, caFile      string
	imapHost, imapSecurity, imapUser, imapPass string
	imapFolder                                 string
	imapPort                                   int
	poll                                       time.Duration
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil && n > 0 {
		return n
	}
	return def
}

func loadMailConfig() mailExtConfig {
	c := mailExtConfig{
		smtpHost: envOr("MAIL_SMTP_HOST", ""), smtpSecurity: strings.ToLower(envOr("MAIL_SMTP_SECURITY", "starttls")),
		smtpUser: envOr("MAIL_SMTP_USER", ""), smtpPass: os.Getenv("MAIL_SMTP_PASSWORD"),
		fromMode: strings.ToLower(envOr("MAIL_FROM_MODE", "user")), fromAddress: envOr("MAIL_FROM_ADDRESS", ""),
		domain: envOr("MAIL_DOMAIN", "exim.local"), caFile: envOr("MAIL_CA_FILE", ""),
		imapHost: envOr("MAIL_IMAP_HOST", ""), imapSecurity: strings.ToLower(envOr("MAIL_IMAP_SECURITY", "tls")),
		imapUser: envOr("MAIL_IMAP_USER", ""), imapPass: os.Getenv("MAIL_IMAP_PASSWORD"), imapFolder: envOr("MAIL_IMAP_FOLDER", "INBOX"),
		poll: time.Duration(envInt("MAIL_POLL_SECONDS", 30)) * time.Second,
	}
	c.smtpPort = envInt("MAIL_SMTP_PORT", map[string]int{"tls": 465, "starttls": 587, "none": 25}[c.smtpSecurity])
	c.imapPort = envInt("MAIL_IMAP_PORT", map[string]int{"tls": 993, "starttls": 143, "none": 143}[c.imapSecurity])
	return c
}

var mailCfg = loadMailConfig()

func (c mailExtConfig) outbound() bool { return c.smtpHost != "" }
func (c mailExtConfig) inbound() bool  { return c.imapHost != "" }

func (c mailExtConfig) tlsConfig(host string) (*tls.Config, error) {
	cfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	if c.caFile != "" { // the bank's internal CA
		pem, err := os.ReadFile(c.caFile)
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("MAIL_CA_FILE contains no certificate")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// startMailExternal runs the queue and the poller when the corresponding settings are present.
func (a *app) startMailExternal(ctx context.Context, log *slog.Logger) {
	if mailCfg.outbound() {
		log.Info("external mail: outgoing relay enabled", "host", mailCfg.smtpHost, "port", mailCfg.smtpPort, "security", mailCfg.smtpSecurity, "from_mode", mailCfg.fromMode)
		go a.mailOutboxLoop(ctx, log)
	}
	if mailCfg.inbound() {
		log.Info("external mail: incoming mailbox enabled", "host", mailCfg.imapHost, "port", mailCfg.imapPort, "security", mailCfg.imapSecurity, "folder", mailCfg.imapFolder, "poll", mailCfg.poll.String())
		go a.mailInboundLoop(ctx, log)
	}
}

// ---------- external recipients ----------

type extRcpt struct{ address, name, kind string }

// resolveExternal parses the typed addresses. An address that belongs to a platform user is delivered internally
// (it is added to kinds); the others are returned as external recipients.
func (a *app) resolveExternal(ctx context.Context, req mailDraftReq, kinds map[int64]string) ([]extRcpt, error) {
	var cands []extRcpt
	seen := map[string]bool{}
	for _, set := range []struct {
		list []string
		kind string
	}{{req.ToExt, "to"}, {req.CcExt, "cc"}, {req.BccExt, "bcc"}} {
		for _, raw := range set.list {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			ad, err := mail.ParseAddress(raw)
			if err != nil || len(ad.Address) > 254 || strings.ContainsAny(ad.Address, "\r\n") {
				return nil, bad("invalid e-mail address: " + raw)
			}
			addr := strings.ToLower(ad.Address)
			if seen[addr] {
				continue
			}
			seen[addr] = true
			cands = append(cands, extRcpt{address: addr, name: ad.Name, kind: set.kind})
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	addrs := make([]string, len(cands))
	for i, c := range cands {
		addrs[i] = c.address
	}
	internal := map[string]int64{}
	if rows, err := a.st.db.Query(ctx, `SELECT id, lower(email::text) FROM users WHERE active AND lower(email::text) = ANY($1)`, addrs); err == nil {
		for rows.Next() {
			var id int64
			var e string
			if rows.Scan(&id, &e) == nil {
				internal[e] = id
			}
		}
		rows.Close()
	}
	var out []extRcpt
	for _, c := range cands {
		if id, ok := internal[c.address]; ok {
			if _, dup := kinds[id]; !dup {
				kinds[id] = c.kind
			}
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// ---------- outgoing ----------

var outboxBackoff = []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

func (a *app) mailOutboxLoop(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		a.processOutbox(ctx, log)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *app) processOutbox(ctx context.Context, log *slog.Logger) {
	for i := 0; i < 20; i++ {
		var id, msgID int64
		var attempts int
		// claim one due item (postponed by 5 minutes so that a crash does not lose it nor send it twice at once)
		err := a.st.db.QueryRow(ctx, `
			UPDATE mail_outbox SET next_attempt_at = now() + interval '5 minutes'
			WHERE id = (SELECT id FROM mail_outbox WHERE status='pending' AND next_attempt_at <= now() ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED)
			RETURNING id, message_id, attempts`).Scan(&id, &msgID, &attempts)
		if err != nil {
			return
		}
		derr := a.deliverExternal(ctx, msgID)
		if derr == nil {
			_, _ = a.st.db.Exec(ctx, `UPDATE mail_outbox SET status='sent', sent_at=now(), attempts=attempts+1, last_error=NULL WHERE id=$1`, id)
			a.auditMail(ctx, msgID, "mail_external_sent", "")
			continue
		}
		attempts++
		var tp *textproto.Error
		permanent := errors.As(derr, &tp) && tp.Code >= 500
		if permanent || attempts >= len(outboxBackoff) {
			_, _ = a.st.db.Exec(ctx, `UPDATE mail_outbox SET status='failed', attempts=$2, last_error=$3 WHERE id=$1`, id, attempts, derr.Error())
			a.auditMail(ctx, msgID, "mail_external_failed", derr.Error())
			a.bounce(ctx, msgID, derr.Error())
			log.Warn("external mail failed", "message", msgID, "err", derr)
			continue
		}
		wait := outboxBackoff[attempts-1]
		_, _ = a.st.db.Exec(ctx, `UPDATE mail_outbox SET attempts=$2, last_error=$3, next_attempt_at=now()+$4::interval WHERE id=$1`, id, attempts, derr.Error(), fmt.Sprintf("%d seconds", int(wait.Seconds())))
		log.Warn("external mail will be retried", "message", msgID, "attempt", attempts, "err", derr)
	}
}

func (a *app) auditMail(ctx context.Context, msgID int64, action, detail string) {
	var sender *int64
	_ = a.st.db.QueryRow(ctx, `SELECT sender_id FROM mail_messages WHERE id=$1`, msgID).Scan(&sender)
	d := map[string]any{"message_id": msgID}
	if detail != "" {
		d["error"] = detail
	}
	a.st.audit(ctx, sender, "", action, "", d)
}

// bounce tells the sender, in their inbox, that an external delivery failed.
func (a *app) bounce(ctx context.Context, msgID int64, reason string) {
	var sender int64
	var sealed []byte
	if a.st.db.QueryRow(ctx, `SELECT sender_id, subject FROM mail_messages WHERE id=$1 AND sender_id IS NOT NULL`, msgID).Scan(&sender, &sealed) != nil {
		return
	}
	subject := a.mailOpen("subject", msgID, sealed)
	var rcpts []string
	if rows, err := a.st.db.Query(ctx, `SELECT address FROM mail_external_recipients WHERE message_id=$1 ORDER BY address`, msgID); err == nil {
		for rows.Next() {
			var s string
			if rows.Scan(&s) == nil {
				rcpts = append(rcpts, s)
			}
		}
		rows.Close()
	}
	body := fmt.Sprintf("Your message \"%s\" could not be delivered to: %s\n\nReason: %s\n\nThe other recipients (if any) received it.", subject, strings.Join(rcpts, ", "), reason)
	_ = a.deliverSystem(ctx, sender, "Delivery failure: "+subject, body)
}

// deliverSystem files a notice from the mail system in a user's inbox.
func (a *app) deliverSystem(ctx context.Context, uid int64, subject, body string) error {
	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id int64
	if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('mail_messages','id'))`).Scan(&id); err != nil {
		return err
	}
	sj, e1 := a.mailSeal("subject", id, subject)
	bd, e2 := a.mailSeal("body", id, body)
	if e1 != nil || e2 != nil {
		return errors.New("seal")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_messages(id, thread_id, source, external_name, external_address, subject, body, sent_at) VALUES ($1,$1,'system','Exim Connect',$2,$3,$4,now())`,
		id, "mailer-daemon@"+mailCfg.domain, sj, bd); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_boxes(message_id, user_id, role, folder, is_read) VALUES ($1,$2,'recipient','inbox',FALSE)`, id, uid); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	a.hub.publish(ctx, []int64{uid}, "mail", map[string]any{"id": id, "thread_id": id, "from_id": 0, "from": "Exim Connect"})
	return nil
}

func newHeaderID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "<" + hex.EncodeToString(b) + "@" + mailCfg.domain + ">"
}

func cleanHeader(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(s))
}

func addrList(ps []*mail.Address) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.String()
	}
	return strings.Join(parts, ", ")
}

// deliverExternal builds the RFC 5322 message and hands it to the SMTP relay.
func (a *app) deliverExternal(ctx context.Context, msgID int64) error {
	var senderID int64
	var senderName, senderEmail string
	var sjSealed, bdSealed []byte
	var parent *int64
	var headerID *string
	err := a.st.db.QueryRow(ctx, `
		SELECT m.sender_id, COALESCE(u.display_name,''), COALESCE(u.email::text,''), m.subject, m.body, m.parent_id, m.header_id
		FROM mail_messages m JOIN users u ON u.id=m.sender_id WHERE m.id=$1`, msgID).Scan(&senderID, &senderName, &senderEmail, &sjSealed, &bdSealed, &parent, &headerID)
	if err != nil {
		return fmt.Errorf("message not found: %w", err)
	}
	subject, body := a.mailOpen("subject", msgID, sjSealed), a.mailOpen("body", msgID, bdSealed)

	// sender address
	from := &mail.Address{Name: senderName, Address: senderEmail}
	var replyTo []*mail.Address
	if mailCfg.fromMode == "fixed" || senderEmail == "" {
		if mailCfg.fromAddress == "" {
			return errors.New("no sender address: the user has no e-mail and MAIL_FROM_ADDRESS is not set")
		}
		from = &mail.Address{Name: senderName, Address: mailCfg.fromAddress}
		if senderEmail != "" {
			replyTo = []*mail.Address{{Name: senderName, Address: senderEmail}}
		}
	}

	// recipients: everybody is shown in To/Cc (internal people by their e-mail address); Bcc stays out of the headers
	var to, cc []*mail.Address
	var envelope []string
	if rows, err := a.st.db.Query(ctx, `SELECT u.display_name, COALESCE(u.email::text,''), r.kind FROM mail_recipients r JOIN users u ON u.id=r.user_id WHERE r.message_id=$1 ORDER BY u.display_name`, msgID); err == nil {
		for rows.Next() {
			var n, e, k string
			if rows.Scan(&n, &e, &k) == nil && e != "" {
				ad := &mail.Address{Name: n, Address: e}
				if k == "to" {
					to = append(to, ad)
				} else if k == "cc" {
					cc = append(cc, ad)
				}
			}
		}
		rows.Close()
	}
	if rows, err := a.st.db.Query(ctx, `SELECT address, COALESCE(name,''), kind FROM mail_external_recipients WHERE message_id=$1 ORDER BY address`, msgID); err == nil {
		for rows.Next() {
			var e, n, k string
			if rows.Scan(&e, &n, &k) == nil {
				envelope = append(envelope, e)
				ad := &mail.Address{Name: n, Address: e}
				if k == "to" {
					to = append(to, ad)
				} else if k == "cc" {
					cc = append(cc, ad)
				}
			}
		}
		rows.Close()
	}
	if len(envelope) == 0 {
		return nil // nothing external left to deliver
	}

	hid := ""
	if headerID != nil {
		hid = *headerID
	} else {
		hid = newHeaderID()
		_, _ = a.st.db.Exec(ctx, `UPDATE mail_messages SET header_id=$2 WHERE id=$1 AND header_id IS NULL`, msgID, hid)
	}
	var parentHeader string
	if parent != nil {
		_ = a.st.db.QueryRow(ctx, `SELECT COALESCE(header_id,'') FROM mail_messages WHERE id=$1`, *parent).Scan(&parentHeader)
	}

	var buf bytes.Buffer
	h := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, cleanHeader(v)) }
	h("From", from.String())
	if len(replyTo) > 0 {
		h("Reply-To", addrList(replyTo))
	}
	if len(to) > 0 {
		h("To", addrList(to))
	}
	if len(cc) > 0 {
		h("Cc", addrList(cc))
	}
	h("Subject", mime.QEncoding.Encode("utf-8", cleanHeader(subject)))
	h("Date", time.Now().Format(time.RFC1123Z))
	h("Message-ID", hid)
	if parentHeader != "" {
		h("In-Reply-To", parentHeader)
		h("References", parentHeader)
	}
	h("MIME-Version", "1.0")
	h("X-Mailer", "Exim Connect")

	text := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	type att struct{ blob, name, mt string }
	var atts []att
	if rows, err := a.st.db.Query(ctx, `SELECT blob_id::text, filename, mime FROM mail_attachments WHERE message_id=$1 ORDER BY created_at`, msgID); err == nil {
		for rows.Next() {
			var x att
			if rows.Scan(&x.blob, &x.name, &x.mt) == nil {
				atts = append(atts, x)
			}
		}
		rows.Close()
	}
	writeText := func(w io.Writer) error {
		qp := quotedprintable.NewWriter(w)
		if _, err := qp.Write([]byte(text)); err != nil {
			return err
		}
		return qp.Close()
	}
	if len(atts) == 0 {
		h("Content-Type", "text/plain; charset=utf-8")
		h("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeText(&buf); err != nil {
			return err
		}
	} else {
		mw := multipart.NewWriter(&buf)
		h("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
		buf.WriteString("\r\n")
		pw, err := mw.CreatePart(textproto.MIMEHeader{"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return err
		}
		if err := writeText(pw); err != nil {
			return err
		}
		for _, x := range atts {
			plain, err := a.mailBlob(ctx, x.blob)
			if err != nil {
				return fmt.Errorf("attachment %s: %w", x.name, err)
			}
			pw, err := mw.CreatePart(textproto.MIMEHeader{
				"Content-Type":              {mime.FormatMediaType("application/octet-stream", map[string]string{"name": x.name})},
				"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": x.name})},
				"Content-Transfer-Encoding": {"base64"},
			})
			if err != nil {
				return err
			}
			enc := base64.StdEncoding.EncodeToString(plain)
			for len(enc) > 76 {
				_, _ = pw.Write([]byte(enc[:76] + "\r\n"))
				enc = enc[76:]
			}
			_, _ = pw.Write([]byte(enc + "\r\n"))
		}
		if err := mw.Close(); err != nil {
			return err
		}
	}
	return smtpSend(mailCfg, from.Address, envelope, buf.Bytes())
}

// mailBlob reads and decrypts an attachment object.
func (a *app) mailBlob(ctx context.Context, blob string) ([]byte, error) {
	obj, err := a.s3.GetObject(ctx, attachBucket, "mail/"+blob, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	sealed, err := io.ReadAll(io.LimitReader(obj, maxUpload+1024))
	if err != nil {
		return nil, err
	}
	return a.box.Open(sealed, []byte(blob))
}

func smtpSend(cfg mailExtConfig, from string, rcpts []string, data []byte) error {
	addr := net.JoinHostPort(cfg.smtpHost, strconv.Itoa(cfg.smtpPort))
	tcfg, err := cfg.tlsConfig(cfg.smtpHost)
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if cfg.smtpSecurity == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, tcfg)
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	c, err := smtp.NewClient(conn, cfg.smtpHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if cfg.smtpSecurity == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server does not offer STARTTLS")
		}
		if err := c.StartTLS(tcfg); err != nil {
			return err
		}
	}
	if cfg.smtpUser != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg.smtpUser, cfg.smtpPass, cfg.smtpHost)); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range rcpts {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ---------- incoming ----------

func (a *app) mailInboundLoop(ctx context.Context, log *slog.Logger) {
	for {
		if err := a.pollIMAP(ctx, log); err != nil {
			log.Warn("external mail: polling failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(mailCfg.poll):
		}
	}
}

func dialIMAP(cfg mailExtConfig) (*client.Client, error) {
	addr := net.JoinHostPort(cfg.imapHost, strconv.Itoa(cfg.imapPort))
	tcfg, err := cfg.tlsConfig(cfg.imapHost)
	if err != nil {
		return nil, err
	}
	switch cfg.imapSecurity {
	case "tls":
		return client.DialWithDialerTLS(&net.Dialer{Timeout: 20 * time.Second}, addr, tcfg)
	default:
		c, err := client.DialWithDialer(&net.Dialer{Timeout: 20 * time.Second}, addr)
		if err != nil {
			return nil, err
		}
		if cfg.imapSecurity == "starttls" {
			if err := c.StartTLS(tcfg); err != nil {
				c.Logout() //nolint:errcheck
				return nil, err
			}
		}
		return c, nil
	}
}

// pollIMAP files every unread message of the mailbox, then flags it as read.
func (a *app) pollIMAP(ctx context.Context, log *slog.Logger) error {
	c, err := dialIMAP(mailCfg)
	if err != nil {
		return err
	}
	defer c.Logout() //nolint:errcheck
	c.Timeout = 2 * time.Minute
	if err := c.Login(mailCfg.imapUser, mailCfg.imapPass); err != nil {
		return err
	}
	mbox, err := c.Select(mailCfg.imapFolder, false)
	if err != nil {
		return err
	}
	if mbox.Messages == 0 {
		return nil
	}
	crit := imap.NewSearchCriteria()
	crit.WithoutFlags = []string{imap.SeenFlag}
	uids, err := c.UidSearch(crit)
	if err != nil || len(uids) == 0 {
		return err
	}
	if len(uids) > 50 {
		uids = uids[:50]
	}
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true}
	msgs := make(chan *imap.Message, 10)
	done := make(chan error, 1)
	go func() { done <- c.UidFetch(set, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, msgs) }()
	type raw struct {
		uid  uint32
		data []byte
	}
	var fetched []raw
	for m := range msgs {
		if r := m.GetBody(section); r != nil {
			b, _ := io.ReadAll(io.LimitReader(r, 40<<20))
			fetched = append(fetched, raw{m.Uid, b})
		}
	}
	if err := <-done; err != nil {
		return err
	}
	seen := new(imap.SeqSet)
	for _, f := range fetched {
		if err := a.ingestMail(ctx, f.data, log); err != nil {
			log.Warn("external mail: message not filed, will be retried", "uid", f.uid, "err", err)
			continue
		}
		seen.AddNum(f.uid)
	}
	if !seen.Empty() {
		return c.UidStore(seen, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.SeenFlag}, nil)
	}
	return nil
}

var (
	tagRE      = regexp.MustCompile(`(?s)<(script|style)[^>]*>.*?</(script|style)>|<[^>]+>`)
	breakRE    = regexp.MustCompile(`(?i)<br\s*/?>|</p>|</div>|</tr>|</h[1-6]>|</li>`)
	blankRunRE = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(s string) string {
	s = breakRE.ReplaceAllString(s, "\n")
	s = tagRE.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(blankRunRE.ReplaceAllString(s, "\n\n"))
}

func headerAddrs(h gomail.Header, keys ...string) []*mail.Address {
	var out []*mail.Address
	for _, k := range keys {
		f := h.FieldsByKey(k)
		for f.Next() {
			v := strings.TrimSpace(f.Value())
			if ads, err := mail.ParseAddressList(v); err == nil {
				out = append(out, ads...)
			} else if ad, err := mail.ParseAddress(strings.Trim(v, " <>")); err == nil {
				out = append(out, ad)
			}
		}
	}
	return out
}

func msgIDs(v string) []string {
	var out []string
	for _, p := range strings.Fields(v) {
		if strings.HasPrefix(p, "<") && strings.HasSuffix(p, ">") {
			out = append(out, p)
		}
	}
	return out
}

// ingestMail files one received message. It returns nil when the message was filed or deliberately skipped
// (no platform recipient, duplicate), and an error when it should be retried.
func (a *app) ingestMail(ctx context.Context, raw []byte, log *slog.Logger) error {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		log.Warn("external mail: unreadable message skipped", "err", err)
		return nil
	}
	h := mr.Header
	froms := headerAddrs(h, "From")
	if len(froms) == 0 {
		log.Warn("external mail: message without From skipped")
		return nil
	}
	subject, _ := h.Subject()
	subject = strings.TrimSpace(subject)
	date, derr := h.Date()
	if derr != nil || date.IsZero() || date.After(time.Now().Add(24*time.Hour)) {
		date = time.Now()
	}
	headerID := ""
	if ids := msgIDs(h.Get("Message-Id")); len(ids) > 0 {
		headerID = ids[0]
	}
	inReply := msgIDs(h.Get("In-Reply-To") + " " + h.Get("References"))

	toList, ccList := headerAddrs(h, "To"), headerAddrs(h, "Cc")
	delivered := headerAddrs(h, "Delivered-To", "X-Original-To", "Envelope-To", "X-Envelope-To", "Resent-To")
	kindOf := map[string]string{}
	for _, ad := range delivered {
		kindOf[strings.ToLower(ad.Address)] = "bcc"
	}
	for _, ad := range ccList {
		kindOf[strings.ToLower(ad.Address)] = "cc"
	}
	for _, ad := range toList {
		kindOf[strings.ToLower(ad.Address)] = "to"
	}
	addrs := make([]string, 0, len(kindOf))
	for k := range kindOf {
		addrs = append(addrs, k)
	}
	type user struct {
		id   int64
		addr string
	}
	var users []user
	rows, err := a.st.db.Query(ctx, `SELECT id, lower(email::text) FROM users WHERE active AND lower(email::text) = ANY($1)`, addrs)
	if err != nil {
		return err
	}
	for rows.Next() {
		var u user
		if rows.Scan(&u.id, &u.addr) == nil {
			users = append(users, u)
		}
	}
	rows.Close()
	if len(users) == 0 {
		log.Info("external mail: no platform recipient, skipped", "from", froms[0].Address, "subject", subject)
		return nil
	}
	if headerID != "" {
		var dup bool
		if a.st.db.QueryRow(ctx, `SELECT true FROM mail_messages WHERE header_id=$1`, headerID).Scan(&dup) == nil {
			return nil
		}
	}

	// body and attachments
	var text, htmlBody string
	type received struct {
		name, mt string
		data     []byte
	}
	var files []received
	var total int64
	for {
		p, err := mr.NextPart()
		if err != nil {
			break // io.EOF, or a damaged tail: keep what was read
		}
		switch ph := p.Header.(type) {
		case *gomail.InlineHeader:
			ct, _, _ := ph.ContentType()
			b, _ := io.ReadAll(io.LimitReader(p.Body, 2<<20))
			if ct == "text/plain" && text == "" {
				text = string(b)
			} else if ct == "text/html" && htmlBody == "" {
				htmlBody = string(b)
			}
		case *gomail.AttachmentHeader:
			name, _ := ph.Filename()
			name = cleanFilename(name)
			b, _ := io.ReadAll(io.LimitReader(p.Body, maxUpload+1))
			if int64(len(b)) > maxUpload || len(b) == 0 || mailBlockedExt[strings.ToLower(path.Ext(name))] || len(files) >= mailMaxFiles || total+int64(len(b)) > mailMaxTotal {
				continue // dropped: too big, empty, executable, or over the limits
			}
			ct, _, _ := ph.ContentType()
			total += int64(len(b))
			files = append(files, received{name: name, mt: ct, data: b})
		}
	}
	if text == "" && htmlBody != "" {
		text = htmlToText(htmlBody)
	}
	if len(text) > mailMaxBody {
		text = text[:mailMaxBody]
	}

	tx, err := a.st.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var id int64
	if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('mail_messages','id'))`).Scan(&id); err != nil {
		return err
	}
	thread := id
	var parent *int64
	if len(inReply) > 0 {
		var pid, pth int64
		if tx.QueryRow(ctx, `SELECT id, thread_id FROM mail_messages WHERE header_id = ANY($1) ORDER BY id DESC LIMIT 1`, inReply).Scan(&pid, &pth) == nil {
			parent, thread = &pid, pth
		}
	}
	if len([]rune(subject)) > mailMaxSubject {
		subject = string([]rune(subject)[:mailMaxSubject])
	}
	sj, e1 := a.mailSeal("subject", id, subject)
	bd, e2 := a.mailSeal("body", id, text)
	if e1 != nil || e2 != nil {
		return errors.New("seal")
	}
	var hid *string
	if headerID != "" {
		hid = &headerID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO mail_messages(id, thread_id, parent_id, source, external_name, external_address, header_id, subject, body, sent_at)
		VALUES ($1,$2,$3,'inbound',NULLIF($4,''),$5,$6,$7,$8,$9)`, id, thread, parent, froms[0].Name, strings.ToLower(froms[0].Address), hid, sj, bd, date); err != nil {
		return err
	}
	matched := map[string]bool{}
	var notify []int64
	for _, u := range users {
		matched[u.addr] = true
		if _, err := tx.Exec(ctx, `INSERT INTO mail_recipients(message_id, user_id, kind) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, id, u.id, kindOf[u.addr]); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_boxes(message_id, user_id, role, folder, is_read) VALUES ($1,$2,'recipient','inbox',FALSE) ON CONFLICT DO NOTHING`, id, u.id); err != nil {
			return err
		}
		notify = append(notify, u.id)
	}
	for _, ad := range append(append([]*mail.Address{}, toList...), ccList...) { // shown in the headers but not on the platform
		l := strings.ToLower(ad.Address)
		if matched[l] {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO mail_external_recipients(message_id, address, name, kind) VALUES ($1,$2,NULLIF($3,''),$4) ON CONFLICT DO NOTHING`, id, l, ad.Name, kindOf[l]); err != nil {
			return err
		}
	}
	for _, f := range files {
		var aid, blob string
		if err := tx.QueryRow(ctx, `INSERT INTO mail_attachments(blob_id, message_id, filename, mime, size) VALUES (gen_random_uuid(), $1,$2,$3,$4) RETURNING id::text, blob_id::text`,
			id, f.name, sniffMIME(f.data), int64(len(f.data))).Scan(&aid, &blob); err != nil {
			return err
		}
		sealed, err := a.box.Seal(f.data, []byte(blob))
		if err != nil {
			return err
		}
		if _, err := a.s3.PutObject(ctx, attachBucket, "mail/"+blob, bytes.NewReader(sealed), int64(len(sealed)), minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	name := froms[0].Name
	if name == "" {
		name = froms[0].Address
	}
	a.hub.publish(ctx, notify, "mail", map[string]any{"id": id, "thread_id": thread, "from_id": 0, "from": name})
	return nil
}

func sniffMIME(b []byte) string { return http.DetectContentType(b[:min(512, len(b))]) }
