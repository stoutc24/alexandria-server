package nowplaying

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// Announcements: the party's PA system. A device in the party records a
// short voice message, and every speaking device dips its music, plays a
// chime and then the voice at the same instant on the party clock.
//
// The SERVER picks every instant; devices only read them. One
// announcement at a time per party, rate limited per profile, and the
// bytes held across every profile are capped. A clip is held in memory
// only — never the database, disk or logs — readable only by its own
// party, and zeroed RetainAfterEndMs after it has played (a timer, and a
// sweep on every read), or at once when the party ends or expires.

// Announcement constants: the wire contract's section 8.
const (
	// ClipChimeLeadMs: a clip's chime starts this long after the upload
	// arrives — time for every parked poll to wake and fetch it first.
	ClipChimeLeadMs = 1400
	// ChimeMs is the chime's length; ChimeGapMs the quiet before a clip's
	// voice.
	ChimeMs    = 1000
	ChimeGapMs = 100
	// AnnounceSampleRate is every announcement's PCM rate (mono, s16le).
	AnnounceSampleRate = 16000
	// MaxClipBytes, MinClipMs and MaxClipMs bound one clip.
	MaxClipBytes = 2 * 1024 * 1024
	MinClipMs    = 300
	MaxClipMs    = 60_500
	// MaxClipBytesTotal caps the clip bytes every party holds together.
	MaxClipBytesTotal = 32 * 1024 * 1024
	// AnnounceStartsPerMinute caps a profile's announcements in a
	// rolling minute.
	AnnounceStartsPerMinute = 6
	// An announcement is LISTED on the state until this long past its
	// end, and the party is BUSY until this long past it.
	AnnounceListedAfterEndMs = 3000
	AnnounceBusyAfterEndMs   = 1000
	// RetainAfterEndMs: an announcement's bytes are zeroed this long past
	// its end, for a device that fetches late.
	RetainAfterEndMs = 30_000

	// KindClip is a recorded message.
	KindClip = "clip"

	wavHeaderLen     = 44
	announceWindowMs = 60_000
)

// announcement is the party's one announcement. Caller holds the store's
// write lock for everything here.
type announcement struct {
	id, kind         string
	byToken, byLabel string
	chimeAtMs        int64
	voiceStartsAtMs  int64
	durationMs       int64
	voiceEndsAtMs    int64
	sizeBytes        int
	stoppedAtMs      int64
	stoppedByLabel   string
	// clip is the uploaded WAV (the handler's own copy).
	clip []byte
	// cleanup zeroes it all RetainAfterEndMs past its end.
	cleanup *time.Timer
}

// AnnouncementView is an announcement on the wire (state.announcement,
// and the answers that start or end one).
type AnnouncementView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	ByLabel     string `json:"byLabel"`
	ByDeviceKey string `json:"byDeviceKey"`
	// Voice sample k is heard at VoiceStartsAtMs + k/16 on the party
	// clock, on every device; chime sample j at ChimeAtMs + j/16.
	ChimeAtMs       int64 `json:"chimeAtMs"`
	VoiceStartsAtMs int64 `json:"voiceStartsAtMs"`
	SampleRate      int   `json:"sampleRate"`
	// A clip's length and size.
	DurationMs    int64 `json:"durationMs,omitempty"`
	VoiceEndsAtMs int64 `json:"voiceEndsAtMs,omitempty"`
	SizeBytes     int   `json:"sizeBytes,omitempty"`
	// Stopped early, by the seat holder or the sender.
	StoppedAtMs    int64  `json:"stoppedAtMs,omitempty"`
	StoppedByLabel string `json:"stoppedByLabel,omitempty"`
}

// effectiveEnd is when the announcement stops sounding: when it was
// stopped, else its voice's end.
func (ann *announcement) effectiveEnd() (int64, bool) {
	if ann.stoppedAtMs > 0 {
		return ann.stoppedAtMs, true
	}
	return ann.voiceEndsAtMs, true
}

// listed: still on the state.
func (ann *announcement) listed(nowMs int64) bool {
	end, known := ann.effectiveEnd()
	return !known || nowMs < end+AnnounceListedAfterEndMs
}

// busy: no other announcement may start.
func (ann *announcement) busy(nowMs int64) bool {
	end, known := ann.effectiveEnd()
	return !known || nowMs < end+AnnounceBusyAfterEndMs
}

// over: its voice has ended on its own, so a stop changes nothing.
func (ann *announcement) over(nowMs int64) bool {
	return nowMs >= ann.voiceEndsAtMs
}

func (ann *announcement) view() AnnouncementView {
	return AnnouncementView{
		ID:              ann.id,
		Kind:            ann.kind,
		ByLabel:         ann.byLabel,
		ByDeviceKey:     ann.byToken,
		ChimeAtMs:       ann.chimeAtMs,
		VoiceStartsAtMs: ann.voiceStartsAtMs,
		SampleRate:      AnnounceSampleRate,
		DurationMs:      ann.durationMs,
		VoiceEndsAtMs:   ann.voiceEndsAtMs,
		SizeBytes:       ann.sizeBytes,
		StoppedAtMs:     ann.stoppedAtMs,
		StoppedByLabel:  ann.stoppedByLabel,
	}
}

// ParseClipWAV checks a clip is EXACTLY the one format the party plays: a
// canonical little-endian WAV with a 44-byte header — PCM, mono,
// 16000 Hz, 16-bit — whose RIFF and data sizes match the file. Nothing
// else is decoded, and nothing else is accepted, so no device ever feeds
// an uploaded container to a platform decoder. code is "" for a good
// clip, else "bad-audio", "too-short" (under MinClipMs) or "too-large"
// (over MaxClipMs).
func ParseClipWAV(b []byte) (durationMs int64, code string) {
	n := int64(len(b))
	if n < wavHeaderLen {
		return 0, "bad-audio"
	}
	le := binary.LittleEndian
	data := n - wavHeaderLen
	if string(b[0:4]) != "RIFF" || int64(le.Uint32(b[4:8])) != n-8 ||
		string(b[8:12]) != "WAVE" || string(b[12:16]) != "fmt " ||
		le.Uint32(b[16:20]) != 16 || le.Uint16(b[20:22]) != 1 || le.Uint16(b[22:24]) != 1 ||
		le.Uint32(b[24:28]) != AnnounceSampleRate || le.Uint32(b[28:32]) != 2*AnnounceSampleRate ||
		le.Uint16(b[32:34]) != 2 || le.Uint16(b[34:36]) != 16 ||
		string(b[36:40]) != "data" || int64(le.Uint32(b[40:44])) != data || data%2 != 0 {
		return 0, "bad-audio"
	}
	durationMs = data / 32
	switch {
	case durationMs < MinClipMs:
		return durationMs, "too-short"
	case durationMs > MaxClipMs:
		return durationMs, "too-large"
	}
	return durationMs, ""
}

// newID is 128 random bits as 32 lowercase hex characters: an
// announcement's id is a key to someone's voice.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // never fails: since Go 1.24 it crashes the program instead
	return hex.EncodeToString(b[:])
}

// canStartLocked: may tokenID start an announcement now? In order: it is
// in the party, the seat holder's leader-only setting lets it, nothing is
// playing (sweeping first), and the profile is under its rate.
func (s *Store) canStartLocked(profileID, tokenID string, st State, now time.Time) Outcome {
	nowMs := now.UnixMilli()
	if !s.inPartyLocked(profileID, tokenID, st, now) {
		return OutcomeNotInParty
	}
	a := s.audio[profileID]
	if a.leaderOnly(st) && tokenID != st.LeaderTokenID {
		return OutcomeLeaderOnly
	}
	s.sweepAnnouncementLocked(profileID, nowMs)
	if a != nil && a.ann != nil && a.ann.busy(nowMs) {
		return OutcomeAnnounceBusy
	}
	if len(s.recentStartsLocked(profileID, nowMs)) >= AnnounceStartsPerMinute {
		return OutcomeRateLimited
	}
	return OutcomeOK
}

// recentStartsLocked prunes the profile's start times to the rolling
// minute and returns them.
func (s *Store) recentStartsLocked(profileID string, nowMs int64) []int64 {
	starts := s.announced[profileID]
	keep := starts[:0:0]
	for _, at := range starts {
		if nowMs-at < announceWindowMs {
			keep = append(keep, at)
		}
	}
	if len(keep) == 0 {
		delete(s.announced, profileID)
	} else {
		s.announced[profileID] = keep
	}
	return keep
}

// StartClip starts a recorded announcement. wav is the caller's own copy
// (the handler copies the request body: Fiber reuses it), already
// checked by ParseClipWAV; the store keeps it, or zeroes it when it
// refuses. The chime starts ClipChimeLeadMs from now and the voice
// ChimeMs + ChimeGapMs after that.
func (s *Store) StartClip(profileID, tokenID, label string, wav []byte, durationMs int64) (State, *AnnouncementView, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		clear(wav)
		return State{}, nil, OutcomeNoSession
	}
	if out := s.canStartLocked(profileID, tokenID, st, now); out != OutcomeOK {
		clear(wav)
		return s.readLocked(profileID, st, nowMs), nil, out
	}
	a := s.audioLocked(profileID)
	// The party's last announcement, if still held, is done playing (it
	// is not busy) and goes when this one starts.
	var replaced int64
	if a.ann != nil {
		replaced = int64(len(a.ann.clip))
	}
	if s.clipBytesTotal-replaced+int64(len(wav)) > MaxClipBytesTotal {
		clear(wav)
		return s.readLocked(profileID, st, nowMs), nil, OutcomeServerBusy
	}
	s.deleteAnnouncementLocked(profileID)

	chime := nowMs + ClipChimeLeadMs
	voice := chime + ChimeMs + ChimeGapMs
	ann := &announcement{
		id: newID(), kind: KindClip, byToken: tokenID, byLabel: label,
		chimeAtMs: chime, voiceStartsAtMs: voice,
		durationMs: durationMs, voiceEndsAtMs: voice + durationMs,
		sizeBytes: len(wav), clip: wav,
	}
	a.ann = ann
	s.clipBytesTotal += int64(len(wav))
	s.announced[profileID] = append(s.announced[profileID], nowMs)
	s.armCleanupLocked(profileID, ann, nowMs)
	v := ann.view()
	return s.partyAudioChangedLocked(profileID, st, now), &v, OutcomeOK
}

// Clip is a copy of a clip's bytes, made under the lock: the response is
// written after the handler returns, while the cleanup may zero the kept
// bytes. Only a device in the clip's party gets it; anyone else — and an
// unknown, expired or live id — gets OutcomeNotFound, which never says
// whether the clip exists.
func (s *Store) Clip(profileID, tokenID, id string) ([]byte, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return nil, OutcomeNoSession
	}
	if !s.inPartyLocked(profileID, tokenID, st, now) {
		return nil, OutcomeNotFound
	}
	s.sweepAnnouncementLocked(profileID, nowMs)
	a := s.audio[profileID]
	if a == nil || a.ann == nil || a.ann.id != id || a.ann.kind != KindClip || len(a.ann.clip) == 0 {
		return nil, OutcomeNotFound
	}
	return append([]byte(nil), a.ann.clip...), OutcomeOK
}

// StopAnnouncement stops the party's announcement now: every device
// ramps its music back up from stoppedAtMs. The seat holder (live or
// dormant) or the sender may. A repeat stop, or one after the voice has
// ended on its own, changes nothing and answers OK without a bump.
func (s *Store) StopAnnouncement(profileID, tokenID, label, id string) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return State{}, OutcomeNoSession
	}
	s.sweepAnnouncementLocked(profileID, nowMs)
	a := s.audio[profileID]
	if a == nil || a.ann == nil || a.ann.id != id {
		return s.readLocked(profileID, st, nowMs), OutcomeNotFound
	}
	ann := a.ann
	if tokenID == "" || (tokenID != st.LeaderTokenID && tokenID != ann.byToken) {
		return s.readLocked(profileID, st, nowMs), OutcomeNotAllowed
	}
	if ann.stoppedAtMs > 0 || ann.over(nowMs) {
		return s.readLocked(profileID, st, nowMs), OutcomeOK
	}
	ann.stoppedAtMs, ann.stoppedByLabel = nowMs, label
	s.armCleanupLocked(profileID, ann, nowMs)
	return s.partyAudioChangedLocked(profileID, st, now), OutcomeOK
}

// armCleanupLocked (re)arms the timer that zeroes the announcement
// RetainAfterEndMs past its end.
func (s *Store) armCleanupLocked(profileID string, ann *announcement, nowMs int64) {
	end, known := ann.effectiveEnd()
	if !known {
		return
	}
	if ann.cleanup != nil {
		ann.cleanup.Stop()
	}
	d := time.Duration(end+RetainAfterEndMs-nowMs) * time.Millisecond
	if d < 0 {
		d = 0
	}
	id := ann.id
	ann.cleanup = time.AfterFunc(d, func() { s.expireAnnouncement(profileID, id) })
}

// expireAnnouncement is the cleanup timer: it deletes the announcement if
// it is still the party's and its time has come. A timer that lost a race
// with a newer announcement, or with a re-arm, does nothing. No bump: an
// announcement leaving is never news.
func (s *Store) expireAnnouncement(profileID, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.audio[profileID]
	if a == nil || a.ann == nil || a.ann.id != id {
		return
	}
	if end, known := a.ann.effectiveEnd(); !known || time.Now().UnixMilli() < end+RetainAfterEndMs {
		return
	}
	s.deleteAnnouncementLocked(profileID)
}

// sweepAnnouncementLocked deletes the announcement once it is past its
// retention: the timer's backstop, on every read.
func (s *Store) sweepAnnouncementLocked(profileID string, nowMs int64) {
	a := s.audio[profileID]
	if a == nil || a.ann == nil {
		return
	}
	if end, known := a.ann.effectiveEnd(); known && nowMs > end+RetainAfterEndMs {
		s.deleteAnnouncementLocked(profileID)
	}
}

// deleteAnnouncementLocked forgets the party's announcement: its bytes
// zeroed in place, the global count given back, its timers stopped.
func (s *Store) deleteAnnouncementLocked(profileID string) {
	a := s.audio[profileID]
	if a == nil || a.ann == nil {
		return
	}
	ann := a.ann
	if ann.cleanup != nil {
		ann.cleanup.Stop()
	}
	s.clipBytesTotal -= int64(len(ann.clip))
	clear(ann.clip)
	ann.clip = nil
	a.ann = nil
}
