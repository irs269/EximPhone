package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/irs269/eximphone/services/pkg/sdpmini"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

type evKind int

const (
	evAttachA   evKind = iota // the calling phone's INVITE arrived
	evBAnswered               // a forked desk phone picked up
	evBDeclined               // a forked desk phone refused or never answered
	evPCAnswered
	evEnded    // the api ended the call (PC hung up, timeout, ...)
	evACancel  // calling phone sent CANCEL while ringing
	evABye     // calling phone hung up
	evBBye     // answering phone hung up
	evShutdown // service stopping
)

type event struct {
	kind      evKind
	leg       *bLeg
	remote    string // phone RTP address from its SDP
	remoteKey []byte // phone SRTP key from its SDP (nil for plain RTP)
}

// bLeg is one forked INVITE toward a registered desk phone of the callee.
type bLeg struct {
	dlg    *sipgo.DialogClientSession
	legID  string
	port   int
	cancel context.CancelFunc
}

// session is the signaling state of one call that involves at least one desk phone.
type session struct {
	id string
	s  *service
	ev chan event

	// calling side when the caller is a desk phone
	aDlg  *sipgo.DialogServerSession
	aLeg  *mediaLeg
	aUser int64
	aSDP  []byte // SDP for the 200 OK to the calling phone

	forkOnce sync.Once
	done     chan struct{}
}

func (s *service) session(callID string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	cs := s.sessions[callID]
	if cs == nil {
		cs = &session{id: callID, s: s, ev: make(chan event, 16), done: make(chan struct{})}
		s.sessions[callID] = cs
		go cs.run()
	}
	return cs
}

func (s *service) lookup(callID string) *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[callID]
}

func (cs *session) post(e event) {
	select {
	case cs.ev <- e:
	case <-cs.done:
	}
}

// ---- incoming INVITE from a desk phone ----

func (s *service) onInvite(req *sip.Request, tx sip.ServerTransaction) {
	dev, ok := s.authenticate(req, tx)
	if !ok {
		return
	}
	// NAT: later requests of this dialog (BYE...) must go to where the phone really is, not to the
	// private address it put in Contact. Replace it with the observed source address.
	if c := req.Contact(); c != nil {
		if host, portStr, err := net.SplitHostPort(req.Source()); err == nil {
			port, _ := strconv.Atoi(portStr)
			c.Address.Host, c.Address.Port = host, port
		}
	}
	dlg, err := s.dialogSrv.ReadInvite(req, tx)
	if err != nil {
		s.log.Warn("read invite", "err", err)
		return
	}
	defer dlg.Close()
	_ = dlg.Respond(sip.StatusTrying, "Trying", nil)
	ctx := context.Background()

	offer, err := sdpmini.Parse(req.Body())
	if err != nil || !offer.Has(sdpmini.PTPCMU) {
		_ = dlg.Respond(sip.StatusNotAcceptableHere, "Not Acceptable Here", nil) // phones must speak G.711 mu-law
		return
	}
	secure := offer.Secure && offer.Key != nil
	s.log.Info("invite received", "ext", dev.Extension, "srtp_offered", offer.Secure, "key", offer.Key != nil, "srtp_required", s.cfg.requireSRTP)
	if s.cfg.requireSRTP && !secure {
		_ = dlg.Respond(sip.StatusNotAcceptableHere, "SRTP required", nil)
		return
	}
	res, code, err := s.api.createCall(ctx, dev.UserID, req.Recipient.User)
	switch {
	case err != nil:
		_ = dlg.Respond(sip.StatusServiceUnavailable, "Service Unavailable", nil)
		return
	case code == 404:
		_ = dlg.Respond(sip.StatusNotFound, "Not Found", nil)
		return
	case code == 409:
		_ = dlg.Respond(sip.StatusBusyHere, "Busy Here", nil)
		return
	case code != 200:
		_ = dlg.Respond(sip.StatusServiceUnavailable, "Service Unavailable", nil)
		return
	}
	if res.State == "ended" { // never rang: map the routing outcome to the SIP answer phones know
		switch res.Outcome {
		case "offline":
			_ = dlg.Respond(sip.StatusTemporarilyUnavailable, "Temporarily Unavailable", nil)
		default: // dnd, busy: SIP 486 Busy Here
			_ = dlg.Respond(sip.StatusBusyHere, "Busy Here", nil)
		}
		return
	}

	leg, err := s.api.newLeg(ctx, res.CallID, dev.UserID, secure)
	if err != nil || s.api.activateLeg(ctx, leg.ID, net.JoinHostPort(offer.Addr, strconv.Itoa(offer.Port)), offer.Key) != nil {
		s.api.hangup(ctx, res.CallID, dev.UserID, "cancelled")
		_ = dlg.Respond(sip.StatusServiceUnavailable, "Service Unavailable", nil)
		return
	}
	cs := s.session(res.CallID)
	s.mu.Lock()
	s.byCallID[req.CallID().Value()] = cs
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.byCallID, req.CallID().Value())
		s.mu.Unlock()
	}()

	_ = dlg.Respond(sip.StatusRinging, "Ringing", nil)
	cs.aDlg, cs.aLeg, cs.aUser = dlg, leg, dev.UserID
	cs.aSDP = s.answerSDP(leg)
	cs.post(event{kind: evAttachA})

	select {
	case <-dlg.Context().Done(): // BYE from the caller (or dialog failure)
		cs.post(event{kind: evABye})
	case <-cs.done:
	}
	<-cs.done
}

func (s *service) onCancel(req *sip.Request, tx sip.ServerTransaction) {
	s.mu.Lock()
	cs := s.byCallID[req.CallID().Value()]
	s.mu.Unlock()
	if cs == nil {
		s.reply(req, tx, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist")
		return
	}
	s.reply(req, tx, sip.StatusOK, "OK")
	cs.post(event{kind: evACancel})
}

func (s *service) onBye(req *sip.Request, tx sip.ServerTransaction) {
	if err := s.dialogSrv.ReadBye(req, tx); err == nil {
		return
	}
	if err := s.dialogCli.ReadBye(req, tx); err == nil {
		return
	}
	s.reply(req, tx, sip.StatusCallTransactionDoesNotExists, "Call/Transaction Does Not Exist")
}

func (s *service) onAck(req *sip.Request, tx sip.ServerTransaction) { _ = s.dialogSrv.ReadAck(req, tx) }

func (s *service) onOptions(req *sip.Request, tx sip.ServerTransaction) {
	s.reply(req, tx, sip.StatusOK, "OK") // keep-alive pings from phones
}

// ---- commands from the api (Redis) ----

type command struct {
	Type         string `json:"type"`
	CallID       string `json:"call_id"`
	CalleeUserID int64  `json:"callee_user_id"`
	From         struct {
		Name      string `json:"display_name"`
		Extension string `json:"extension"`
	} `json:"from"`
}

func (s *service) listen(ctx context.Context) {
	sub := s.st.rdb.Subscribe(ctx, "eximphone:sip")
	defer sub.Close()
	for m := range sub.Channel() {
		var c command
		if json.Unmarshal([]byte(m.Payload), &c) != nil {
			continue
		}
		switch c.Type {
		case "originate": // ring the callee's registered desk phones
			cs := s.session(c.CallID)
			cs.forkOnce.Do(func() { go s.fork(cs, c) })
		case "pc_answered":
			if cs := s.lookup(c.CallID); cs != nil {
				cs.post(event{kind: evPCAnswered})
			}
		case "ended":
			if cs := s.lookup(c.CallID); cs != nil {
				cs.post(event{kind: evEnded})
			}
		}
	}
}

// fork sends an INVITE to every desk phone registered for the callee. The first to answer wins.
func (s *service) fork(cs *session, c command) {
	ctx := context.Background()
	bindings := s.st.contacts(ctx, c.CalleeUserID)
	if len(bindings) == 0 {
		return
	}
	declined := make(chan struct{}, len(bindings))
	for _, b := range bindings {
		go func(b binding) {
			if !s.inviteOne(cs, c, b) {
				declined <- struct{}{}
			}
		}(b)
	}
}

// inviteOne returns true if the phone answered.
func (s *service) inviteOne(cs *session, c command, b binding) bool {
	ctx, cancel := context.WithCancel(context.Background())
	leg, err := s.api.newLeg(ctx, cs.id, c.CalleeUserID, s.cfg.requireSRTP)
	if err != nil {
		cancel()
		return false
	}
	host, portStr, err := net.SplitHostPort(b.Source)
	port, _ := strconv.Atoi(portStr)
	if err != nil {
		cancel()
		s.api.deleteLeg(ctx, leg.ID)
		return false
	}
	recipient := sip.Uri{Scheme: "sip", User: b.Extension, Host: host, Port: port, UriParams: sip.NewParams()}
	recipient.UriParams.Add("transport", b.Transport)

	from := &sip.FromHeader{
		DisplayName: c.From.Name,
		Address:     sip.Uri{Scheme: "sip", User: c.From.Extension, Host: s.cfg.domain},
		Params:      sip.NewParams(),
	}
	from.Params.Add("tag", sip.GenerateTagN(16))
	sdp := s.answerSDP(leg)
	dlg, err := s.dialogCli.Invite(ctx, recipient, sdp, from, sip.NewHeader("Content-Type", "application/sdp"))
	if err != nil {
		cancel()
		s.api.deleteLeg(ctx, leg.ID)
		return false
	}
	defer dlg.Close()
	bl := &bLeg{dlg: dlg, legID: leg.ID, port: leg.Port, cancel: cancel}
	s.mu.Lock()
	cs.addForked(bl)
	s.mu.Unlock()

	// NAT: same rewrite for the phone's Contact in its responses, so ACK and BYE reach it.
	natFix := func(res *sip.Response) error {
		if c := res.Contact(); c != nil {
			c.Address.Host, c.Address.Port = host, port
		}
		return nil
	}
	if err := dlg.WaitAnswer(ctx, sipgo.AnswerOptions{OnResponse: natFix}); err != nil {
		s.log.Info("forked invite ended without answer", "call", cs.id, "ext", b.Extension, "err", err.Error())
		// cancelled by us (another device or the PC answered, caller hung up) or declined (486/603/timeout)
		s.api.deleteLeg(context.Background(), leg.ID)
		cancel()
		return false
	}
	answer, perr := sdpmini.Parse(dlg.InviteResponse.Body())
	if perr == nil && s.cfg.requireSRTP && (!answer.Secure || answer.Key == nil) {
		perr = errNoSRTP // the phone answered without SRTP although it is mandatory
	}
	if perr != nil || dlg.Ack(ctx) != nil {
		_ = dlg.Bye(context.Background())
		s.api.deleteLeg(context.Background(), leg.ID)
		cancel()
		return false
	}
	cs.post(event{kind: evBAnswered, leg: bl, remote: net.JoinHostPort(answer.Addr, strconv.Itoa(answer.Port)), remoteKey: answer.Key})
	select { // the phone hangs up
	case <-dlg.Context().Done():
		cs.post(event{kind: evBBye, leg: bl})
	case <-cs.done:
	}
	return true
}

// forked legs are tracked on the session (guarded by service.mu for simplicity).
var forkedLegs = map[*session][]*bLeg{}

func (cs *session) addForked(b *bLeg) { forkedLegs[cs] = append(forkedLegs[cs], b) }

// ---- per-call state machine ----

func (cs *session) run() {
	s := cs.s
	ctx := context.Background()
	var (
		answered  bool
		winner    *bLeg
		attachedA bool
		answerSDP []byte
	)
	// 200 OK to the calling phone, with SDP pointing at its media leg.
	answerA := func() {
		if cs.aDlg != nil && attachedA && answerSDP == nil {
			answerSDP = cs.aSDP
			_ = cs.aDlg.RespondSDP(answerSDP)
		}
	}
	cancelForked := func(except *bLeg) {
		s.mu.Lock()
		legs := append([]*bLeg(nil), forkedLegs[cs]...)
		s.mu.Unlock()
		for _, b := range legs {
			if b != except {
				b.cancel() // CANCEL if still ringing
			}
		}
	}
	teardown := func(viaAPI bool, by int64, as string) {
		cancelForked(nil)
		if !viaAPI {
			s.api.hangup(ctx, cs.id, by, as)
		}
		if winner != nil {
			_ = winner.dlg.Bye(ctx)
		}
		if cs.aDlg != nil {
			if answered {
				_ = cs.aDlg.Bye(ctx)
			} else if attachedA {
				_ = cs.aDlg.Respond(sip.StatusRequestTerminated, "Request Terminated", nil)
			}
			s.api.deleteLeg(ctx, cs.aLeg.ID)
		}
		s.mu.Lock()
		delete(s.sessions, cs.id)
		delete(forkedLegs, cs)
		s.mu.Unlock()
		close(cs.done)
	}

	for e := range cs.ev {
		switch e.kind {
		case evAttachA:
			attachedA = true
			if answered {
				answerA()
			}
		case evBAnswered:
			if answered { // someone else won already
				_ = e.leg.dlg.Bye(ctx)
				s.api.deleteLeg(ctx, e.leg.legID)
				continue
			}
			// anchor the phone's media first, then tell the api (PCs then join and must find the phone leg)
			if err := s.api.activateLeg(ctx, e.leg.legID, e.remote, e.remoteKey); err != nil {
				_ = e.leg.dlg.Bye(ctx)
				continue
			}
			answered, winner = true, e.leg
			cancelForked(e.leg)
			if err := s.api.answered(ctx, cs.id); err != nil {
				s.log.Warn("api answered", "err", err)
			}
			answerA()
		case evPCAnswered:
			if answered {
				continue
			}
			s.log.Info("pc answered: cancelling forked desk phones", "call", cs.id)
			answered = true
			cancelForked(nil)
			answerA()
		case evEnded:
			teardown(true, 0, "")
			return
		case evACancel:
			teardown(false, cs.aUser, "cancelled")
			return
		case evABye:
			teardown(false, cs.aUser, "cancelled")
			return
		case evBBye:
			if e.leg == winner {
				winner = nil // it hung up: nothing to BYE
				teardown(false, 0, "cancelled")
				return
			}
		case evShutdown:
			teardown(true, 0, "")
			return
		}
	}
}

var errNoSRTP = errors.New("phone answered without SRTP")

// answerSDP builds the SDP we send to a phone for one media leg: SRTP (SDES) when the leg is secure.
func (s *service) answerSDP(leg *mediaLeg) []byte {
	id := time.Now().UnixNano() / 1e6
	if leg.SRTPKey != "" {
		if key, err := base64.StdEncoding.DecodeString(leg.SRTPKey); err == nil {
			return sdpmini.BuildSecure(s.cfg.publicIP, leg.Port, id, key)
		}
	}
	return sdpmini.Build(s.cfg.publicIP, leg.Port, id)
}

func (s *service) String() string { return fmt.Sprintf("sip(%s)", s.cfg.domain) }
