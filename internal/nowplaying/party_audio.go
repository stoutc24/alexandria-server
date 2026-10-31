package nowplaying

import "time"

// Party volume, what each device in the party can do, and who may make
// an announcement (the announcement itself is announce.go).
//
// Kept like queue edits: in a store map beside the state (Store.audio),
// added to the state at READ time, and never decoded from a report — so a
// leader that predates the fields cannot reset them by leaving them out,
// and a body that carries them changes nothing.
//
// Nothing here goes near ApplyIntent. A change bumps the revision and
// wakes every parked poll, and touches no intent, anchor or
// scheduled-start bookkeeping: a leader heartbeat based on the revision
// before it is not refused as stale, and a scheduled track start stands.
//
// Lifetime: kept across reports, hand-overs, a vacant seat and devices
// leaving; dropped with the session — a stop-all, a DELETE that deletes
// it, Get's expiry, and a report that finds the stored session ended or
// stale. Memory only: never the database, disk or logs.

// Party-volume bounds.
const (
	// MaxDeviceVolumes caps the device levels one party keeps. A level is
	// kept per token and only the seat or a live follower can be given
	// one, so no household meets it.
	MaxDeviceVolumes = 64
	// MaxFeatures and MaxFeatureLen bound one device's features list.
	MaxFeatures   = 8
	MaxFeatureLen = 32
	// maxFeatureTokens caps how many tokens' lists one party keeps. Lists
	// outlive a device leaving, and a profile token can mint more tokens;
	// when full, tokens that are neither the seat nor a live device go.
	maxFeatureTokens = MaxFollowersPerProfile
)

// partyAudio is one party's volume and its devices' features. Caller
// holds the store's write lock for everything here.
type partyAudio struct {
	// volume is the party slider (nil = never set, which reads as 100);
	// volumeBy the label of the token that last set it.
	volume   *int
	volumeBy string
	// devices[token] is a device's own position; 100 is no entry.
	devices map[string]int
	// features[token] is what that device said it can do.
	features map[string][]string
	// policyLeaderOnly is the seat holder's "only I may announce",
	// stored with the token that set it (policyBy): it reads true only
	// while that token holds the seat (leaderOnly).
	policyLeaderOnly bool
	policyBy         string
	// ann is the party's announcement, one at a time (announce.go).
	ann *announcement
}

// leaderOnly is the read-time announceLeaderOnly for a state. Nil-safe.
func (a *partyAudio) leaderOnly(st State) bool {
	return a != nil && a.policyLeaderOnly && a.policyBy != "" && a.policyBy == st.LeaderTokenID
}

// deviceVolume is tok's own position, nil for 100. Nil-safe.
func (a *partyAudio) deviceVolume(tok string) *int {
	if a == nil || tok == "" {
		return nil
	}
	v, ok := a.devices[tok]
	if !ok {
		return nil
	}
	return &v
}

// featuresOf is a copy of tok's features, nil when none are known.
// Nil-safe.
func (a *partyAudio) featuresOf(tok string) []string {
	if a == nil || tok == "" || len(a.features[tok]) == 0 {
		return nil
	}
	return append([]string(nil), a.features[tok]...)
}

// audioLocked is the profile's party audio, made on first use.
func (s *Store) audioLocked(profileID string) *partyAudio {
	a := s.audio[profileID]
	if a == nil {
		a = &partyAudio{}
		s.audio[profileID] = a
	}
	return a
}

// dropPartyAudioLocked forgets everything party audio holds for the
// profile, an announcement's bytes zeroed first. Caller holds the write
// lock.
func (s *Store) dropPartyAudioLocked(profileID string) {
	s.deleteAnnouncementLocked(profileID)
	delete(s.audio, profileID)
}

// VolumeFor is tokenID's own device position on this state (nil = 100),
// for the handler's per-response yourDeviceVolume.
func (st State) VolumeFor(tokenID string) *int {
	if tokenID == "" {
		return nil
	}
	v, ok := st.deviceVolumes[tokenID]
	if !ok {
		return nil
	}
	return &v
}

// clearReadTimeAudio zeroes every party-audio field of a state: a report
// body's own values before the state is stored, and the stored state's
// before a read decorates it.
func clearReadTimeAudio(st *State) {
	st.PartyVolume = nil
	st.PartyVolumeByLabel = ""
	st.LeaderVolume = nil
	st.YourDeviceVolume = nil
	st.LeaderFeatures = nil
	st.AnnounceLeaderOnly = false
	st.Announcement = nil
	st.deviceVolumes = nil
}

// decorateAudioLocked adds the party's audio to a state being read.
// Never called for an ended tombstone. Caller holds the write lock.
func (s *Store) decorateAudioLocked(profileID string, st *State, nowMs int64) {
	a := s.audio[profileID]
	if a == nil {
		return
	}
	if a.volume != nil {
		v := *a.volume
		st.PartyVolume = &v
		st.PartyVolumeByLabel = a.volumeBy
	}
	st.LeaderVolume = a.deviceVolume(st.LeaderTokenID)
	st.LeaderFeatures = a.featuresOf(st.LeaderTokenID)
	st.AnnounceLeaderOnly = a.leaderOnly(*st)
	s.sweepAnnouncementLocked(profileID, nowMs)
	if a.ann != nil && a.ann.listed(nowMs) {
		v := a.ann.view()
		st.Announcement = &v
	}
	if len(a.devices) > 0 {
		st.deviceVolumes = make(map[string]int, len(a.devices))
		for tok, v := range a.devices {
			st.deviceVolumes[tok] = v
		}
	}
}

// partySessionLocked is the profile's session for the party-audio
// routes, when it is live: stored, not ended, and not stale by Get's
// rule. A stale session is not revived by them.
func (s *Store) partySessionLocked(profileID string, now time.Time) (State, bool) {
	st, ok := s.m[profileID]
	if !ok || st.Ended || s.staleLocked(st, profileID, now) {
		return State{}, false
	}
	return st, true
}

// inPartyLocked: tokenID holds the seat (live or dormant), or has a live
// presence of any role under its own key or a minted one.
func (s *Store) inPartyLocked(profileID, tokenID string, st State, now time.Time) bool {
	if tokenID == "" {
		return false
	}
	if st.LeaderTokenID == tokenID {
		return true
	}
	_, member := s.memberLocked(profileID, tokenID, now)
	return member
}

// partyAudioChangedLocked bumps the revision for a party-audio change and
// wakes every parked poll — and nothing else: no intent, anchor or
// schedule field moves. Returns the state to answer with.
func (s *Store) partyAudioChangedLocked(profileID string, st State, now time.Time) State {
	st.Revision = s.nextRevLocked(profileID)
	s.m[profileID] = st
	s.lastActivity[profileID] = now
	s.notifyLocked(profileID)
	return s.readLocked(profileID, st, now.UnixMilli())
}

// clampVolume keeps a position in 0–100 (the handler refuses anything
// else; the store never keeps one).
func clampVolume(v int) int {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	}
	return v
}

// SetPartyVolume sets the party slider. Any device in the party may.
// The same value answers OK without a bump.
func (s *Store) SetPartyVolume(profileID, tokenID, label string, v int) (State, Outcome) {
	v = clampVolume(v)
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return State{}, OutcomeNoSession
	}
	if !s.inPartyLocked(profileID, tokenID, st, now) {
		return s.readLocked(profileID, st, nowMs), OutcomeNotInParty
	}
	cur := 100
	if a := s.audio[profileID]; a != nil && a.volume != nil {
		cur = *a.volume
	}
	if v == cur {
		return s.readLocked(profileID, st, nowMs), OutcomeOK
	}
	a := s.audioLocked(profileID)
	a.volume = &v
	a.volumeBy = label
	return s.partyAudioChangedLocked(profileID, st, now), OutcomeOK
}

// SetDeviceVolume sets one device's own position, on top of the party
// volume. deviceKey names the seat (its token id) or a live follower's
// row; the level is kept for that device's TOKEN, so an Apple TV keeps
// it when it moves from following (under a UUID) to leading. 100 removes
// the entry. Any device in the party may.
func (s *Store) SetDeviceVolume(profileID, tokenID, deviceKey string, v int) (State, Outcome) {
	v = clampVolume(v)
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return State{}, OutcomeNoSession
	}
	if !s.inPartyLocked(profileID, tokenID, st, now) {
		return s.readLocked(profileID, st, nowMs), OutcomeNotInParty
	}
	tok, ok := s.deviceTokenLocked(profileID, st, deviceKey, now)
	if !ok {
		return s.readLocked(profileID, st, nowMs), OutcomeNoSuchDevice
	}
	a := s.audio[profileID]
	cur, had := 0, false
	if a != nil {
		cur, had = a.devices[tok]
	}
	switch {
	case v == 100 && !had, had && cur == v:
		return s.readLocked(profileID, st, nowMs), OutcomeOK
	case v == 100:
		delete(a.devices, tok)
	case !had && a != nil && len(a.devices) >= MaxDeviceVolumes:
		return s.readLocked(profileID, st, nowMs), OutcomeServerBusy
	default:
		a = s.audioLocked(profileID)
		if a.devices == nil {
			a.devices = make(map[string]int)
		}
		a.devices[tok] = v
	}
	return s.partyAudioChangedLocked(profileID, st, now), OutcomeOK
}

// SetAnnouncePolicy is the seat holder's "only I may announce" (or not):
// only the seat holder, live or dormant, may set it. It is kept with the
// setter's token and reads true only while that token holds the seat, so
// a leading Apple TV (no microphone) or an older build (no switch) can
// never inherit a party nobody can announce to, or switch back on. An
// unchanged setting answers OK without a bump.
func (s *Store) SetAnnouncePolicy(profileID, tokenID string, leaderOnly bool) (State, Outcome) {
	now := time.Now()
	nowMs := now.UnixMilli()
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.partySessionLocked(profileID, now)
	if !ok {
		return State{}, OutcomeNoSession
	}
	if tokenID == "" || st.LeaderTokenID != tokenID {
		return s.readLocked(profileID, st, nowMs), OutcomeNotLeader
	}
	a := s.audio[profileID]
	if a != nil && a.policyBy == tokenID && a.policyLeaderOnly == leaderOnly {
		return s.readLocked(profileID, st, nowMs), OutcomeOK
	}
	before := a.leaderOnly(st)
	a = s.audioLocked(profileID)
	a.policyLeaderOnly, a.policyBy = leaderOnly, tokenID
	if a.leaderOnly(st) == before {
		// Kept as this holder's choice, but nothing anyone reads moved.
		return s.readLocked(profileID, st, nowMs), OutcomeOK
	}
	return s.partyAudioChangedLocked(profileID, st, now), OutcomeOK
}

// deviceTokenLocked resolves a device volume's deviceKey to the token the
// level is kept for: the seat's own token id, or a live follower row's
// noted token (else its key). Never an empty key: a vacant seat is "".
func (s *Store) deviceTokenLocked(profileID string, st State, deviceKey string, now time.Time) (string, bool) {
	if deviceKey == "" {
		return "", false
	}
	if deviceKey == st.LeaderTokenID {
		return deviceKey, true
	}
	e, ok := s.followers[profileID][deviceKey]
	if !ok || e.role != RoleFollower || now.Sub(e.last) > followerTTL {
		return "", false
	}
	if e.tokenID != "" {
		return e.tokenID, true
	}
	return deviceKey, true
}

// CleanFeatures checks a device's features list: at most MaxFeatures
// entries, each a short lowercase word (^[a-z][a-z0-9-]{0,31}$), with
// duplicates dropped. ok is false when the list is absent or invalid,
// and the caller then treats it as absent — never an error, so a
// heartbeat never fails on it. An empty list is valid: "none".
func CleanFeatures(in []string) ([]string, bool) {
	if in == nil || len(in) > MaxFeatures {
		return nil, false
	}
	out := make([]string, 0, len(in))
	for _, f := range in {
		if !validFeature(f) {
			return nil, false
		}
		dup := false
		for _, have := range out {
			if have == f {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, f)
		}
	}
	return out, true
}

func validFeature(f string) bool {
	if f == "" || len(f) > MaxFeatureLen || f[0] < 'a' || f[0] > 'z' {
		return false
	}
	for i := 1; i < len(f); i++ {
		c := f[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

// setFeaturesLocked keeps tokenID's features (cleaned; empty = none) and
// says whether that changed what is kept. Caller holds the write lock.
func (s *Store) setFeaturesLocked(profileID, tokenID string, list []string) bool {
	if tokenID == "" {
		return false
	}
	a := s.audio[profileID]
	var old []string
	had := false
	if a != nil {
		old, had = a.features[tokenID]
	}
	if len(list) == 0 {
		if !had {
			return false
		}
		delete(a.features, tokenID)
		return true
	}
	if had && equalStrings(old, list) {
		return false
	}
	a = s.audioLocked(profileID)
	if a.features == nil {
		a.features = make(map[string][]string)
	}
	if !had && len(a.features) >= maxFeatureTokens {
		s.pruneFeaturesLocked(profileID, a)
		if len(a.features) >= maxFeatureTokens {
			return false
		}
	}
	a.features[tokenID] = append([]string(nil), list...)
	return true
}

// pruneFeaturesLocked forgets the features of tokens that neither hold
// the seat nor have a live presence entry.
func (s *Store) pruneFeaturesLocked(profileID string, a *partyAudio) {
	now := time.Now()
	live := make(map[string]bool)
	if st, ok := s.m[profileID]; ok && st.LeaderTokenID != "" {
		live[st.LeaderTokenID] = true
	}
	for key, e := range s.followers[profileID] {
		if now.Sub(e.last) > followerTTL {
			continue
		}
		if e.tokenID != "" {
			live[e.tokenID] = true
		} else {
			live[key] = true
		}
	}
	for tok := range a.features {
		if !live[tok] {
			delete(a.features, tok)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
