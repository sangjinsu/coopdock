package room

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/sangjinsu/coopdock/internal/game"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func scopeConfig() Config {
	c := DefaultConfig()
	c.GameIDs = []string{"game-a", "game-b"}
	c.hash = func(p, salt []byte) []byte {
		sum := sha256.Sum256(append(append([]byte{}, p...), salt...))
		return sum[:]
	}
	return c
}
func scopedSession(t *testing.T, m *Manager, peer, gameID string) *Session {
	t.Helper()
	s, err := m.ConnectGame(peer, gameID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func scopedList(t *testing.T, m *Manager, s *Session) []Snapshot {
	t.Helper()
	rows, err := m.ListPublic(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.GameID != s.GameID() {
			t.Fatalf("foreign snapshot: %+v", row)
		}
	}
	return rows
}
func TestGameScopeListAndJoin(t *testing.T) {
	m := setup(t, scopeConfig())
	a, b := scopedSession(t, m, "a", "game-a"), scopedSession(t, m, "b", "game-b")
	v := create(t, m, a, CreateOptions{})
	if got := scopedList(t, m, b); len(got) != 0 {
		t.Fatal("game-b sees game-a room", got)
	}
	_, err := m.Join(context.Background(), b, JoinOptions{RoomNumber: v.RoomNumber})
	code(t, err, "ROOM_NOT_FOUND")
	if v.GameID != "game-a" || a.GameID() != "game-a" {
		t.Fatal("binding missing", v)
	}
}
func TestGameScopeSameNumberCleanupAndCollision(t *testing.T) {
	c := scopeConfig()
	var calls int
	c.number = func() (string, error) {
		calls++
		if calls <= 3 {
			return "004271", nil
		}
		return "004272", nil
	}
	m := setup(t, c)
	a, b, extra := scopedSession(t, m, "a", "game-a"), scopedSession(t, m, "b", "game-b"), scopedSession(t, m, "extra", "game-a")
	av, bv := create(t, m, a, CreateOptions{}), create(t, m, b, CreateOptions{})
	if av.RoomNumber != "004271" || bv.RoomNumber != "004271" || av.RoomID == bv.RoomID {
		t.Fatal("same-number coexistence failed", av, bv)
	}
	other := create(t, m, extra, CreateOptions{})
	if other.RoomNumber != "004272" || calls != 4 {
		t.Fatal("same-game collision was not retried", other, calls)
	}
	m.Disconnect(a)
	bs := scopedList(t, m, b)
	if len(bs) != 1 || bs[0].RoomID != bv.RoomID || bs[0].HostID != b.ID() || len(bs[0].Members) != 1 {
		t.Fatal("A cleanup changed B", bs)
	}
	rows := m.OperationsSnapshot().Rooms
	if len(rows) != 2 || rows[0].GameID != "game-a" || rows[1].GameID != "game-b" {
		t.Fatal("operations scope/sort", rows)
	}
}
func TestGameScopeRegistryAndSessionOwnership(t *testing.T) {
	c := scopeConfig()
	m := setup(t, c)
	c.GameIDs[0] = "mutated"
	if !m.HasGame("game-a") || m.HasGame("mutated") {
		t.Fatal("registry was not copied")
	}
	_, err := m.Connect("legacy")
	code(t, err, "INVALID_MESSAGE")
	for _, id := range []string{"unknown", "GAME-A", ""} {
		_, err = m.ConnectGame("peer", id)
		code(t, err, "INVALID_MESSAGE")
	}
	a := scopedSession(t, m, "a", "game-a")
	foreign := setup(t, scopeConfig())
	other := scopedSession(t, foreign, "foreign", "game-a")
	for _, s := range []*Session{nil, other} {
		_, err = m.ListPublic(context.Background(), s)
		code(t, err, "SESSION_CLOSED")
	}
	fake := &Session{manager: m, id: a.id, gameID: "game-a"}
	_, err = m.ListPublic(context.Background(), fake)
	code(t, err, "SESSION_CLOSED")
	m.Disconnect(a)
	_, err = m.ListPublic(context.Background(), a)
	code(t, err, "SESSION_CLOSED")
	if a.GameID() != "game-a" {
		t.Fatal("disconnect changed immutable binding")
	}
}
func TestGameScopeGameIDValidation(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{{"default", true}, {"a", true}, {"0_-", true}, {strings.Repeat("a", 64), true}, {"", false}, {"-a", false}, {"_a", false}, {"A", false}, {"가", false}, {"a.b", false}, {" a", false}, {strings.Repeat("a", 65), false}} {
		if ValidGameID(tc.id) != tc.valid {
			t.Errorf("valid(%q) != %v", tc.id, tc.valid)
		}
	}
	many := make([]string, 33)
	for i := range many {
		many[i] = fmt.Sprintf("g%d", i)
	}
	for _, ids := range [][]string{nil, {}, {""}, {"a", "a"}, {"A"}, many} {
		c := DefaultConfig()
		c.GameIDs = ids
		m, err := NewManager(c)
		if m != nil {
			m.Close(context.Background())
		}
		if err == nil {
			t.Errorf("accepted invalid registry %v", ids)
		}
	}
	c := DefaultConfig()
	c.GameIDs = many[:32]
	m := setup(t, c)
	if !m.HasGame("g31") {
		t.Fatal("32-game registry rejected")
	}
}
func TestGameScopePrivateErrorOrderAndNoHashLeak(t *testing.T) {
	c := scopeConfig()
	var hashes atomic.Int32
	hash := c.hash
	c.hash = func(p, s []byte) []byte { hashes.Add(1); return hash(p, s) }
	m := setup(t, c)
	a, b := scopedSession(t, m, "a", "game-a"), scopedSession(t, m, "b", "game-b")
	v := create(t, m, a, CreateOptions{Capacity: 1, Visibility: "private", Password: str("abcd")})
	before := hashes.Load()
	for _, password := range []*string{nil, str("wrong"), str("abcd")} {
		_, err := m.Join(context.Background(), b, JoinOptions{RoomNumber: v.RoomNumber, Password: password})
		code(t, err, "ROOM_NOT_FOUND")
	}
	if hashes.Load() != before {
		t.Fatal("cross-game lookup used password pool")
	}
	m.limits.mu.Lock()
	keys := len(m.limits.entries)
	m.limits.mu.Unlock()
	if keys != 0 {
		t.Fatal("cross-game lookup recorded password failures")
	}
	guest := scopedSession(t, m, "guest", "game-a")
	for _, tc := range []struct {
		password *string
		want     string
	}{{nil, "PASSWORD_REQUIRED"}, {str("wrong"), "PASSWORD_INVALID"}, {str("abcd"), "ROOM_FULL"}} {
		_, err := m.Join(context.Background(), guest, JoinOptions{RoomNumber: v.RoomNumber, Password: tc.password})
		code(t, err, tc.want)
	}
	startMatch(t, m, a)
	_, err := m.Join(context.Background(), guest, JoinOptions{RoomNumber: v.RoomNumber, Password: str("abcd")})
	code(t, err, "ROOM_IN_PROGRESS")
}
func TestGameScopeGlobalBounds(t *testing.T) {
	c := scopeConfig()
	c.MaxSessions = 3
	c.MaxRooms = 1
	m := setup(t, c)
	a, b := scopedSession(t, m, "a", "game-a"), scopedSession(t, m, "b", "game-b")
	create(t, m, a, CreateOptions{})
	_, err := m.Create(context.Background(), b, CreateOptions{})
	code(t, err, "SERVER_BUSY")
	scopedSession(t, m, "c", "game-a")
	_, err = m.ConnectGame("d", "game-b")
	code(t, err, "SERVER_BUSY")
	m.Disconnect(a)
	create(t, m, b, CreateOptions{})
}
func TestGameScopeGlobalFailureLimitsAndOpaqueRooms(t *testing.T) {
	for _, mode := range []string{"IP", "room", "cap"} {
		t.Run(mode, func(t *testing.T) {
			c := scopeConfig()
			c.number = func() (string, error) { return "004271", nil }
			c.IPFailureLimit = 100
			c.SessionFailureLimit = 100
			c.RoomFailureLimit = 100
			if mode == "IP" {
				c.IPFailureLimit = 1
			}
			if mode == "room" {
				c.RoomFailureLimit = 1
			}
			if mode == "cap" {
				c.MaxFailureKeys = 3
			}
			m := setup(t, c)
			av := create(t, m, scopedSession(t, m, "ha", "game-a"), CreateOptions{Visibility: "private", Password: str("abcd")})
			bv := create(t, m, scopedSession(t, m, "hb", "game-b"), CreateOptions{Visibility: "private", Password: str("abcd")})
			a := scopedSession(t, m, "shared", "game-a")
			b := scopedSession(t, m, "shared", "game-b")
			_, err := m.Join(context.Background(), a, JoinOptions{RoomNumber: av.RoomNumber, Password: str("wrong")})
			code(t, err, "PASSWORD_INVALID")
			_, err = m.Join(context.Background(), b, JoinOptions{RoomNumber: bv.RoomNumber, Password: str("abcd")})
			if mode == "IP" {
				code(t, err, "RATE_LIMITED")
			} else if err != nil {
				t.Fatal("other opaque room inherited failure", err)
			}
			if mode == "room" {
				_, err = m.Join(context.Background(), a, JoinOptions{RoomNumber: av.RoomNumber, Password: str("abcd")})
				code(t, err, "RATE_LIMITED")
			}
			if mode == "cap" {
				if err = m.Leave(context.Background(), b); err != nil {
					t.Fatal(err)
				}
				_, err = m.Join(context.Background(), b, JoinOptions{RoomNumber: bv.RoomNumber, Password: str("wrong")})
				code(t, err, "PASSWORD_INVALID")
				m.limits.mu.Lock()
				n := len(m.limits.entries)
				m.limits.mu.Unlock()
				if n > 3 {
					t.Fatal("global failure cap multiplied", n)
				}
				_, err = m.Join(context.Background(), a, JoinOptions{RoomNumber: av.RoomNumber, Password: str("abcd")})
				code(t, err, "RATE_LIMITED")
			}
		})
	}
}
func TestGameScopeSharedHashWorkerAndQueue(t *testing.T) {
	c := scopeConfig()
	c.HashWorkers = 1
	c.HashQueue = 1
	b := &barrier{entered: make(chan struct{}, 8), release: make(chan struct{})}
	c.hash = b.hash
	m := setup(t, c)
	t.Cleanup(func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	})
	av := create(t, m, scopedSession(t, m, "ha", "game-a"), CreateOptions{Visibility: "private", Password: str("abcd")})
	bv := create(t, m, scopedSession(t, m, "hb", "game-b"), CreateOptions{Visibility: "private", Password: str("abcd")})
	b.armed.Store(true)
	first := joinAsync(m, context.Background(), scopedSession(t, m, "a", "game-a"), av.RoomNumber)
	await(t, b.entered)
	second := joinAsync(m, context.Background(), scopedSession(t, m, "b", "game-b"), bv.RoomNumber)
	deadline := time.Now().Add(time.Second)
	for len(m.hashJobs) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(m.hashJobs) != 1 {
		t.Fatal("B did not queue behind A worker")
	}
	_, err := m.Join(context.Background(), scopedSession(t, m, "extra", "game-b"), JoinOptions{RoomNumber: bv.RoomNumber, Password: str("abcd")})
	code(t, err, "SERVER_BUSY")
	b.armed.Store(false)
	close(b.release)
	if err = awaitErr(t, first); err != nil {
		t.Fatal(err)
	}
	if err = awaitErr(t, second); err != nil {
		t.Fatal(err)
	}
}

func TestGameScopeListLimitAfterGameFilter(t *testing.T) {
	c := scopeConfig()
	c.ListLimit = 1
	numbers := []string{"000001", "999999"}
	c.number = func() (string, error) { n := numbers[0]; numbers = numbers[1:]; return n, nil }
	m := setup(t, c)
	b, a := scopedSession(t, m, "b", "game-b"), scopedSession(t, m, "a", "game-a")
	create(t, m, b, CreateOptions{})
	av := create(t, m, a, CreateOptions{})
	rows := scopedList(t, m, a)
	if len(rows) != 1 || rows[0].RoomID != av.RoomID {
		t.Fatal("foreign rows consumed game list limit", rows)
	}
}
func TestGameScopeActorMembershipFences(t *testing.T) {
	c := scopeConfig()
	c.number = func() (string, error) { return "004271", nil }
	m := setup(t, c)
	a, b := scopedSession(t, m, "a", "game-a"), scopedSession(t, m, "b", "game-b")
	create(t, m, a, CreateOptions{})
	create(t, m, b, CreateOptions{})
	m.mu.Lock()
	ra, rb := m.rooms[roomKey{"game-a", "004271"}], m.rooms[roomKey{"game-b", "004271"}]
	a.mu.Lock()
	token := a.membershipToken
	a.room = rb
	a.mu.Unlock()
	m.mu.Unlock()
	_, err := m.Start(context.Background(), a)
	code(t, err, "NOT_IN_ROOM")
	err = m.SubmitInput(context.Background(), a, "foreign", Input{MatchID: "match", InputSeq: 1, Action: game.Action{Type: "move"}})
	code(t, err, "NOT_IN_ROOM")
	err = m.Leave(context.Background(), a)
	code(t, err, "NOT_IN_ROOM")
	if rb.remove(a, rb.id, token) {
		t.Fatal("foreign actor removed membership")
	}
	m.mu.Lock()
	a.mu.Lock()
	a.room = ra
	a.mu.Unlock()
	m.mu.Unlock()
	cmd := command{s: a, g: token, ctx: context.Background()}
	m.mu.Lock()
	a.mu.Lock()
	err = rb.startError(cmd)
	a.mu.Unlock()
	m.mu.Unlock()
	code(t, err, "NOT_IN_ROOM")
	err = rb.validateReceipt(receipt{s: a, roomID: rb.id, token: token, deadline: time.Now().Add(time.Second)}, map[*Session]uint64{})
	code(t, err, "INPUT_CANCELED")
	a.mu.Lock()
	a.joining = true
	a.mu.Unlock()
	v := rb.admit(command{s: a, g: token})
	code(t, v.err, "ROOM_NOT_FOUND")
	a.mu.Lock()
	a.joining = false
	a.mu.Unlock()
	if rows := scopedList(t, m, b); len(rows) != 1 || len(rows[0].Members) != 1 || rows[0].HostID != b.id {
		t.Fatal("foreign requests changed B", rows)
	}
}
func TestGameScopeLeaveRejectsReplacedIndex(t *testing.T) {
	m := setup(t, scopeConfig())
	a := scopedSession(t, m, "a", "game-a")
	v := create(t, m, a, CreateOptions{})
	m.mu.Lock()
	key := roomKey{"game-a", v.RoomNumber}
	r := m.rooms[key]
	delete(m.rooms, key)
	m.mu.Unlock()
	err := m.Leave(context.Background(), a)
	m.mu.Lock()
	m.rooms[key] = r
	m.mu.Unlock()
	if err == nil {
		t.Fatal("leave accepted an actor outside composite index")
	}
	code(t, err, "NOT_IN_ROOM")
}
func TestGameScopeDisconnectRequiresSessionIdentity(t *testing.T) {
	m := setup(t, scopeConfig())
	a := scopedSession(t, m, "a", "game-a")
	v := create(t, m, a, CreateOptions{})
	fake := &Session{manager: m, id: a.id, gameID: "game-a", done: make(chan struct{})}
	m.Disconnect(fake)
	rows, err := m.ListPublic(context.Background(), a)
	if err != nil || len(rows) != 1 || rows[0].RoomID != v.RoomID {
		t.Fatal("forged session disconnected live identity", rows, err)
	}
}

func TestGameScopePendingLeaveInvalidatesAdmission(t *testing.T) {
	for _, operation := range []string{"join", "create"} {
		for _, laterCreate := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/later-create=%v", operation, laterCreate), func(t *testing.T) {
				barrier := &barrier{entered: make(chan struct{}, 1), release: make(chan struct{})}
				cfg := scopeConfig()
				cfg.hash = barrier.hash
				m := setup(t, cfg)
				t.Cleanup(func() {
					select {
					case <-barrier.release:
					default:
						close(barrier.release)
					}
				})
				host := scopedSession(t, m, "host", "game-a")
				var original Snapshot
				if operation == "join" {
					original = create(t, m, host, CreateOptions{Visibility: "private", Password: str("abcd")})
				}
				guest := scopedSession(t, m, "guest", "game-a")
				barrier.armed.Store(true)
				pending := make(chan error, 1)
				go func() {
					var err error
					if operation == "join" {
						_, err = m.Join(context.Background(), guest, JoinOptions{RoomNumber: original.RoomNumber, Password: str("abcd")})
					} else {
						_, err = m.Create(context.Background(), guest, CreateOptions{Visibility: "private", Password: str("abcd")})
					}
					pending <- err
				}()
				await(t, barrier.entered)
				code(t, m.Leave(context.Background(), guest), "NOT_IN_ROOM")
				code(t, m.Leave(context.Background(), guest), "NOT_IN_ROOM")
				var later Snapshot
				if laterCreate {
					later = create(t, m, guest, CreateOptions{})
				}
				barrier.armed.Store(false)
				close(barrier.release)
				code(t, awaitErr(t, pending), "ALREADY_IN_ROOM")
				awaitOperations(t, m, func(v OperationsSnapshot) bool {
					expectedRooms := 0
					if operation == "join" {
						expectedRooms++
					}
					if laterCreate {
						expectedRooms++
					}
					if len(v.Rooms) != expectedRooms {
						return false
					}
					for _, row := range v.Rooms {
						if row.GameID != "game-a" || row.MemberCount != 1 {
							return false
						}
						switch row.RoomID {
						case original.RoomID:
							if operation != "join" || row.HostID != host.ID() {
								return false
							}
						case later.RoomID:
							if !laterCreate || row.HostID != guest.ID() {
								return false
							}
						default:
							return false
						}
					}
					return true
				})
				gotID, gotToken := guest.Membership()
				if laterCreate {
					if gotID != later.RoomID || gotToken == 0 {
						t.Fatal("old admission replaced later membership", gotID, gotToken, later)
					}
				} else if gotID != "" || gotToken != 0 {
					t.Fatal("leave did not cancel pending membership", gotID, gotToken)
				}
			})
		}
	}
}
