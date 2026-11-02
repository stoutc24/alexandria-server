package nowplaying

import (
	"context"
	"encoding/binary"
	"time"
)

// Live announcements: hold to talk live. The talker's voice comes in as
// chunks of PCM (16 kHz mono s16le) and goes out to every listening
// device, which plays voice sample k at voiceStartsAtMs + k/16 on the
// party clock — the same sample at the same instant everywhere, about a
// second after it was spoken.
//
// The relay is per session: its own append-only runs and its own wake
// channel. A chunk wakes that session's readers and nobody else; it never
// bumps the party's revision or wakes the party's long-polls. Only the
// start, the end and a stop are party news.
//
// Like a clip, live voice is held in memory only — never the database,
// disk or logs — served only to the profile's own party, dropped LiveRetainMs
// after it was due to be heard, and zeroed RetainAfterEndMs after the end
// or at once when the party ends or expires.

// Live constants: the wire contract's section 8.
const (
	// KindLive is a live announcement.
	KindLive = "live"
	// LiveChimeLeadMs: the chime starts this long after POST /live
	// arrives; capture starts LiveTailMs after the chime ends, so the room's
	// chime is not recorded; the voice is heard LiveDelayMs after it is
	// captured.
	LiveChimeLeadMs = 1000
	LiveTailMs      = 150
	LiveDelayMs     = 1000
	// LiveMaxMs bounds one live announcement: maxEndsAtMs is
	// voiceStartsAtMs + LiveMaxMs, and MaxLiveSamples is its samples.
	LiveMaxMs      = 120_000
	MaxLiveSamples = LiveMaxMs * samplesPerMs
	// MaxLiveChunkBytes caps one chunk from the talker.
	MaxLiveChunkBytes = 65_536
	// LiveRetainMs: a sample is dropped this long after it was due.
	LiveRetainMs = 3000
	// LiveHold is how long a reader waits for samples it does not have.
	LiveHold = 1500 * time.Millisecond
	// MaxLiveWaitersPerSession: readers parked on one session at once —
	// a pool of its own, apart from the profile's long-polls.
	MaxLiveWaitersPerSession = 32
	// MaxLiveFrameSamples caps one answer to a reader.
	MaxLiveFrameSamples = 32_000
	// LiveAheadSlackMs: a chunk may start at most this far past what the
	// talker can have captured by now.
	LiveAheadSlackMs = 2000
	// MaxLiveSessions caps the live announcements open at once, across
	// every profile.
	MaxLiveSessions = 16

	samplesPerMs = AnnounceSampleRate / 1000
)

// liveIdleEnd: a live announcement that hears nothing for this long ends
// itself at what has arrived. A variable so a test can shorten it.
var liveIdleEnd = 5 * time.Second

// liveRun is samples [from, from+len(pcm)/2) of the talker's voice.
type liveRun struct {
	from int64
	pcm  []byte
}

// liveBuf is one live announcement's relay. Caller holds the store's
// write lock for everything here.
type liveBuf struct {
	// runs are what arrived, in order and never overlapping; a sample in
	// no run (the talker skipped it) reads as silence.
	runs []liveRun
	// total is one past the last sample received.
	total int64
	// kept: samples below it have been dropped (LiveRetainMs).
	kept int64
	// wake is closed — and replaced — to wake every parked reader.
	wake    chan struct{}
	waiting int
	// watchdog ends the announcement at idleAt.
	watchdog *time.Timer
	idleAt   time.Time
}

func (lb *liveBuf) wakeAll() {
	close(lb.wake)
	lb.wake = make(chan struct{})
}

const (
	// liveRunSamples is the longest a run grows by merging (1 s): trim
	// drops whole runs, so runs this short keep what is held close to
	// LiveRetainMs.
	liveRunSamples = 1000 * samplesPerMs
	// liveGapFill: a gap shorter than this (100 ms) is filled with the
	// silence it reads as anyway, so the samples after it join the last
	// run instead of starting one. Longer gaps take that long to make
	// (a chunk is never ahead of the clock), which bounds the runs in
	// LiveRetainMs to a few dozen.
	liveGapFill = 100 * samplesPerMs
)

// add keeps samples [start, start+len(pcm)/2), start at or past every run:
// a COPY of just those bytes, joined to the last run when it follows it
// within liveGapFill. Keeping a slice of the request body instead held the
// whole body, so a talker resending 64 KiB to add one sample at a time
// could make the server hold far more than it ever heard (security review,
// 2026-11-02: 125 MiB for 69 KB of voice). An array outgrown is zeroed.
func (lb *liveBuf) add(start int64, pcm []byte) {
	if n := len(lb.runs); n > 0 {
		last := &lb.runs[n-1]
		have := int64(len(last.pcm) / 2)
		gap := start - (last.from + have)
		if gap >= 0 && gap < liveGapFill && have+gap+int64(len(pcm)/2) <= liveRunSamples {
			old := last.pcm
			grown := len(old) + int(gap)*2 + len(pcm)
			if grown > cap(old) {
				buf := make([]byte, len(old), min(max(2*cap(old), grown), 2*liveRunSamples))
				copy(buf, old)
				clear(old)
				old = buf
			}
			last.pcm = old[:grown]
			clear(last.pcm[len(old) : grown-len(pcm)])
			copy(last.pcm[grown-len(pcm):], pcm)
			return
		}
	}
	lb.runs = append(lb.runs, liveRun{from: start, pcm: append([]byte(nil), pcm...)})
}

// trim drops whole runs heard more than LiveRetainMs ago, bytes zeroed —
// never past what has arrived.
func (lb *liveBuf) trim(nowMs, voiceStartsAtMs int64) {
	cut := (nowMs - voiceStartsAtMs - LiveRetainMs) * samplesPerMs
	if cut > lb.total {
		cut = lb.total
	}
	if cut <= lb.kept {
		return
	}
	lb.kept = cut
	i := 0
	for i < len(lb.runs) && lb.runs[i].from+int64(len(lb.runs[i].pcm)/2) <= cut {
		clear(lb.runs[i].pcm)
		i++
	}
	if i > 0 {
		lb.runs = append([]liveRun(nil), lb.runs[i:]...)
	}
}

// cutAt zeroes and drops every sample from end on: a voice that ended
// there is never served past it.
func (lb *liveBuf) cutAt(end int64) {
	var keep []liveRun
	for _, r := range lb.runs {
		rEnd := r.from + int64(len(r.pcm)/2)
		switch {
		case r.from >= end:
			clear(r.pcm)
		case rEnd > end:
			n := (end - r.from) * 2
			clear(r.pcm[n:])
			r.pcm = r.pcm[:n]
			keep = append(keep, r)
		default:
			keep = append(keep, r)
		}
	}
	lb.runs = keep
}

// StartLive opens a live announcement for tokenID, the talker: the same
// refusals as a clip, then OutcomeServerBusy when MaxLiveSessions are
// open. The chime starts LiveChimeLeadMs from now; capture starts at
// captureFromMs, after the chime and its tail; voice sample k is heard at
// voiceStartsAtMs + k/16, LiveDelayMs after it was captured.
func (s *Store) StartLive(profileID, tokenID, label string) (State, *AnnouncementView, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return State{}, nil, OutcomeNoSession
	}
	if out := s.canStartLocked(profileID, tokenID, st, now); out != OutcomeOK {
		return s.readLocked(profileID, st, nowMs), nil, out
	}
	if s.liveSessions >= MaxLiveSessions {
		return s.readLocked(profileID, st, nowMs), nil, OutcomeServerBusy
	}
	a := s.audioLocked(profileID)
	s.deleteAnnouncementLocked(profileID) // the last one is done (not busy)

	chime := nowMs + LiveChimeLeadMs
	capture := chime + ChimeMs + LiveTailMs
	voice := capture + LiveDelayMs
	ann := &announcement{
		id: newID(), kind: KindLive, byToken: tokenID, byLabel: label,
		chimeAtMs: chime, captureFromMs: capture, voiceStartsAtMs: voice,
		maxEndsAtMs: voice + LiveMaxMs,
		live:        &liveBuf{wake: make(chan struct{})},
	}
	a.ann = ann
	s.liveSessions++
	s.announced[profileID] = append(s.announced[profileID], nowMs)
	// Nothing ever coming ends it liveIdleEnd after capture should start.
	s.armWatchdogLocked(profileID, ann, now, time.UnixMilli(capture).Add(liveIdleEnd))
	v := ann.view()
	return s.partyAudioChangedLocked(profileID, st, now), &v, OutcomeOK
}

// armWatchdogLocked (re)arms the timer that ends a live announcement at
// idleAt — never later than maxEndsAtMs: nothing can be heard past it,
// so a talker that keeps sending cannot hold the party busy without end.
func (s *Store) armWatchdogLocked(profileID string, ann *announcement, now, idleAt time.Time) {
	if hard := time.UnixMilli(ann.maxEndsAtMs); idleAt.After(hard) {
		idleAt = hard
	}
	lb := ann.live
	lb.idleAt = idleAt
	d := idleAt.Sub(now)
	if d < 0 {
		d = 0
	}
	if lb.watchdog != nil {
		lb.watchdog.Reset(d)
		return
	}
	id := ann.id
	lb.watchdog = time.AfterFunc(d, func() { s.liveIdle(profileID, id) })
}

// liveIdle is the watchdog: it ends the announcement at what has arrived
// when it is still open and nothing has come since idleAt was set. A
// timer that lost a race with a chunk, an end or a newer announcement
// does nothing.
func (s *Store) liveIdle(profileID, id string) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.audio[profileID]
	if a == nil || a.ann == nil || a.ann.id != id || !a.ann.open() || now.Before(a.ann.live.idleAt) {
		return
	}
	s.endLiveLocked(profileID, a.ann, a.ann.live.total, now.UnixMilli())
	if st, ok := s.m[profileID]; ok && !st.Ended {
		// Party news — every device's duck comes up — but no device
		// acted, so the session's activity clock is left alone.
		st.Revision = s.nextRevLocked(profileID)
		s.m[profileID] = st
		s.notifyLocked(profileID)
	}
}

// endLiveLocked ends an open live announcement at endSample (clamped to
// what arrived): its voice ends at voiceStartsAtMs + ceil(endSample/16),
// nothing past endSample is ever served, its readers wake, and its bytes
// are zeroed RetainAfterEndMs after the end. The caller bumps.
func (s *Store) endLiveLocked(profileID string, ann *announcement, endSample, nowMs int64) {
	if !ann.open() {
		return
	}
	lb := ann.live
	endSample = min(max(endSample, 0), lb.total)
	ann.ended = true
	ann.endSample = endSample
	ann.voiceEndsAtMs = ann.voiceStartsAtMs + (endSample+samplesPerMs-1)/samplesPerMs
	if lb.watchdog != nil {
		lb.watchdog.Stop()
	}
	lb.cutAt(endSample)
	lb.wakeAll()
	s.liveSessions--
	s.armCleanupLocked(profileID, ann, nowMs)
}

// liveLocked is the party's live announcement id, for tokenID: the
// talker (talkerOnly) or any device in the party. Anything else — an
// unknown id, a clip, another profile's, a device that may not — is
// OutcomeNotFound.
func (s *Store) liveLocked(profileID, tokenID, id string, talkerOnly bool, now time.Time) (*announcement, Outcome) {
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return nil, OutcomeNoSession
	}
	a := s.audio[profileID]
	if tokenID == "" || a == nil || a.ann == nil || a.ann.id != id || a.ann.kind != KindLive {
		return nil, OutcomeNotFound
	}
	if talkerOnly && a.ann.byToken != tokenID {
		return nil, OutcomeNotFound
	}
	if !talkerOnly && !s.inPartyLocked(profileID, tokenID, st, now) {
		return nil, OutcomeNotFound
	}
	return a.ann, OutcomeOK
}

// AppendLive takes the talker's samples [from, from+len(pcm)/2). pcm is
// the caller's own copy; the store keeps what it uses and zeroes the
// rest. A retry is idempotent (an overlap is discarded), skipped samples
// read as silence, and samples past MaxLiveSamples are dropped and end
// it. Never bumps the party's revision — except that end — and never
// wakes the party's long-polls: only this session's readers.
//
// endSample is set with OutcomeLiveEnded (it has ended, or was stopped).
func (s *Store) AppendLive(profileID, tokenID, id string, from int64, pcm []byte) (total, endSample int64, out Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	ann, out := s.liveLocked(profileID, tokenID, id, true, now)
	if out != OutcomeOK {
		clear(pcm)
		return 0, 0, out
	}
	lb := ann.live
	if !ann.open() {
		clear(pcm)
		return lb.total, ann.endSample, OutcomeLiveEnded
	}
	if from > (nowMs-ann.captureFromMs+LiveAheadSlackMs)*samplesPerMs {
		clear(pcm)
		return lb.total, 0, OutcomeAhead
	}

	start, stop := from, min(from+int64(len(pcm)/2), MaxLiveSamples)
	start = max(start, lb.total)
	if start < stop {
		lo, hi := (start-from)*2, (stop-from)*2
		lb.add(start, pcm[lo:hi])
	}
	clear(pcm)
	lb.total = max(lb.total, stop)
	lb.trim(nowMs, ann.voiceStartsAtMs)
	lb.wakeAll()

	if lb.total >= MaxLiveSamples {
		s.endLiveLocked(profileID, ann, lb.total, nowMs)
		st := s.m[profileID]
		s.partyAudioChangedLocked(profileID, st, now)
		return lb.total, ann.endSample, OutcomeOK
	}
	s.armWatchdogLocked(profileID, ann, now, now.Add(liveIdleEnd))
	return lb.total, 0, OutcomeOK
}

// LiveRead is one answer to a reader: PCM samples [From, From+len/2),
// silence where the talker skipped.
type LiveRead struct {
	From int64
	PCM  []byte
	// Total is what has arrived so far, or the final count once ended.
	Total int64
	// Ended: nothing past Total will come. Stopped: stop playing now.
	// Skipped: the samples asked for were already dropped, so From is
	// past the from asked.
	Ended, Stopped, Skipped bool
}

// ReadLive answers a reader at once when it holds a sample at or past
// from, or the announcement has ended; otherwise it holds the request up
// to hold for a chunk. At most MaxLiveWaitersPerSession are parked per
// announcement — past that a reader is answered at once, with nothing —
// in a pool apart from the profile's long-polls. A reader must be in the
// party; anyone else gets OutcomeNotFound.
func (s *Store) ReadLive(ctx context.Context, profileID, tokenID, id string, from int64, hold time.Duration) (LiveRead, Outcome) {
	s.mu.Lock()
	now := time.Now()
	ann, out := s.liveLocked(profileID, tokenID, id, false, now)
	if out != OutcomeOK {
		s.mu.Unlock()
		return LiveRead{}, out
	}
	lb := ann.live
	lb.trim(now.UnixMilli(), ann.voiceStartsAtMs)
	if !ann.open() || lb.total > max(from, lb.kept) || lb.waiting >= MaxLiveWaitersPerSession {
		r := liveFrameLocked(ann, from)
		s.mu.Unlock()
		return r, OutcomeOK
	}
	wake := lb.wake
	lb.waiting++
	s.mu.Unlock()

	timer := time.NewTimer(hold)
	select {
	case <-wake:
	case <-timer.C:
	case <-ctx.Done():
	}
	timer.Stop()

	s.mu.Lock()
	defer s.mu.Unlock()
	lb.waiting--
	now = time.Now()
	if ann, out = s.liveLocked(profileID, tokenID, id, false, now); out != OutcomeOK {
		return LiveRead{}, out
	}
	ann.live.trim(now.UnixMilli(), ann.voiceStartsAtMs)
	return liveFrameLocked(ann, from), OutcomeOK
}

// liveFrameLocked builds a reader's answer from from: at most
// MaxLiveFrameSamples, copied, silence for gaps; nothing once stopped.
func liveFrameLocked(ann *announcement, from int64) LiveRead {
	lb := ann.live
	limit := lb.total
	if ann.ended {
		limit = ann.endSample
	}
	r := LiveRead{From: from, Total: limit, Ended: ann.ended, Stopped: ann.stoppedAtMs > 0}
	if from < lb.kept {
		r.From, r.Skipped = lb.kept, true
	}
	end := min(limit, r.From+MaxLiveFrameSamples)
	if r.Stopped || end <= r.From {
		return r
	}
	r.PCM = make([]byte, (end-r.From)*2)
	for _, run := range lb.runs {
		rs, re := run.from, run.from+int64(len(run.pcm)/2)
		if lo, hi := max(rs, r.From), min(re, end); lo < hi {
			copy(r.PCM[(lo-r.From)*2:], run.pcm[(lo-rs)*2:(hi-rs)*2])
		}
	}
	return r
}

// EndLive is the talker ending its live announcement at endSample
// (default: what has arrived; clamped to it). Once only: a repeat
// answers OK without a bump.
func (s *Store) EndLive(profileID, tokenID, id string, endSample *int64) (State, *AnnouncementView, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	ann, out := s.liveLocked(profileID, tokenID, id, true, now)
	if out != OutcomeOK {
		return State{}, nil, out
	}
	st := s.m[profileID]
	if !ann.open() {
		v := ann.view()
		return s.readLocked(profileID, st, nowMs), &v, OutcomeOK
	}
	end := ann.live.total
	if endSample != nil {
		end = *endSample
	}
	s.endLiveLocked(profileID, ann, end, nowMs)
	v := ann.view()
	return s.partyAudioChangedLocked(profileID, st, now), &v, OutcomeOK
}

// EncodeFrame is a LiveRead on the wire, "PAL1", little-endian:
//
//	0..3   "PAL1"
//	4..11  int64  fromSample (index 0 is heard at voiceStartsAtMs)
//	12..19 int64  totalSamples (received so far, or the final count)
//	20..23 uint32 flags: bit0 ended, bit1 stopped, bit2 skipped
//	24..   s16le mono samples, possibly none
func EncodeFrame(r LiveRead) []byte {
	le := binary.LittleEndian
	out := make([]byte, 24+len(r.PCM))
	copy(out, "PAL1")
	le.PutUint64(out[4:], uint64(r.From))
	le.PutUint64(out[12:], uint64(r.Total))
	var flags uint32
	if r.Ended {
		flags |= 1
	}
	if r.Stopped {
		flags |= 2
	}
	if r.Skipped {
		flags |= 4
	}
	le.PutUint32(out[20:], flags)
	copy(out[24:], r.PCM)
	return out
}
