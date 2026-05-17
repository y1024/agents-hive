package master

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/chef-guo/agents-hive/internal/auth"
	"github.com/chef-guo/agents-hive/internal/store"
)

// testStore 用于 user_access 测试的 mock store
type testStore struct {
	sessions map[string]*store.SessionRecord
}

func newTestStore(sessions ...*store.SessionRecord) *testStore {
	m := &testStore{sessions: make(map[string]*store.SessionRecord)}
	for _, s := range sessions {
		m.sessions[s.ID] = s
	}
	return m
}

func (s *testStore) CreateSession(_ context.Context, r *store.SessionRecord) error {
	s.sessions[r.ID] = r
	return nil
}
func (s *testStore) SaveSession(_ context.Context, r *store.SessionRecord) error {
	s.sessions[r.ID] = r
	return nil
}
func (s *testStore) LoadSession(_ context.Context, id string) (*store.SessionRecord, error) {
	r, ok := s.sessions[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return r, nil
}
func (s *testStore) DeleteSession(_ context.Context, id string) error {
	delete(s.sessions, id)
	return nil
}
func (s *testStore) ListSessions(_ context.Context) ([]*store.SessionRecord, error) {
	var rs []*store.SessionRecord
	for _, r := range s.sessions {
		rs = append(rs, r)
	}
	return rs, nil
}
func (s *testStore) ListSessionsByUser(_ context.Context, userID string, _ bool) ([]*store.SessionRecord, error) {
	if userID == "" {
		return []*store.SessionRecord{}, nil
	}
	var rs []*store.SessionRecord
	for _, r := range s.sessions {
		if r.UserID == userID {
			rs = append(rs, r)
		}
	}
	return rs, nil
}
func (s *testStore) GetLastActiveSession(_ context.Context) (*store.SessionRecord, error) {
	return nil, store.ErrNotFound
}
func (s *testStore) AddMessage(_ context.Context, _, _, _ string, _ map[string]any) error {
	return nil
}
func (s *testStore) GetMessages(_ context.Context, _ string, _ int) ([]store.MessageRecord, error) {
	return nil, nil
}
func (s *testStore) ForkSession(_ context.Context, parentID string, forkPoint int, newSessionID, newName, userID string) error {
	return nil
}
func (s *testStore) RevertSession(_ context.Context, _ string, _ int) error         { return nil }
func (s *testStore) UpsertSessionPref(_ context.Context, _, _ string, _ bool) error { return nil }
func (s *testStore) GetSessionStarred(_ context.Context, _, _ string) (bool, error) {
	return false, nil
}
func (s *testStore) UpdateSessionTags(_ context.Context, _ string, _ []string) error { return nil }
func (s *testStore) Close() error                                                    { return nil }

func newMasterForAccessTest(t *testing.T, st store.SessionStore) *Master {
	t.Helper()
	m := &Master{
		sessionMgr: NewSessionManager(make(chan struct{}), zap.NewNop()),
		store:      st,
		logger:     zap.NewNop(),
	}
	return m
}

func TestCheckSessionAccess(t *testing.T) {
	userA := &auth.User{ID: "user-a", Role: "user", Status: "active"}
	userB := &auth.User{ID: "user-b", Role: "user", Status: "active"}
	admin := &auth.User{ID: "admin-1", Role: "admin", Status: "active"}

	sessionA := &store.SessionRecord{ID: "sess-a", UserID: "user-a"}
	sessionLegacy := &store.SessionRecord{ID: "sess-legacy", UserID: ""}

	st := newTestStore(sessionA, sessionLegacy)
	m := newMasterForAccessTest(t, st)

	tests := []struct {
		name      string
		ctx       context.Context
		sessionID string
		wantErr   bool
	}{
		{
			name:      "auth 未启用 → 放行",
			ctx:       context.Background(),
			sessionID: "sess-a",
			wantErr:   false,
		},
		{
			name:      "auth 启用 + 未登录 → 拒绝",
			ctx:       auth.WithAuthEnabled(context.Background()),
			sessionID: "sess-a",
			wantErr:   true,
		},
		{
			name:      "owner 访问自己的 → 放行",
			ctx:       auth.WithUser(auth.WithAuthEnabled(context.Background()), userA),
			sessionID: "sess-a",
			wantErr:   false,
		},
		{
			name:      "非 owner 访问别人的 → 拒绝",
			ctx:       auth.WithUser(auth.WithAuthEnabled(context.Background()), userB),
			sessionID: "sess-a",
			wantErr:   true,
		},
		{
			name:      "admin 访问别人的 → 拒绝（admin 也只能看自己的）",
			ctx:       auth.WithUser(auth.WithAuthEnabled(context.Background()), admin),
			sessionID: "sess-a",
			wantErr:   true,
		},
		{
			name:      "任何人访问遗留无主 → 拒绝（无主 session 不可见）",
			ctx:       auth.WithUser(auth.WithAuthEnabled(context.Background()), userB),
			sessionID: "sess-legacy",
			wantErr:   true,
		},
		{
			// Phase 4 fix: IM 路径第一条消息时 session 未持久化，auth 未启用应放行
			name:      "auth 未启用 + session 不存在 → 放行（IM 路径）",
			ctx:       context.Background(),
			sessionID: "im-feishu-nonexistent",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.checkSessionAccess(tt.ctx, tt.sessionID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestListAllSessions_UserIsolation(t *testing.T) {
	userA := &auth.User{ID: "user-a", Role: "user", Status: "active"}
	userB := &auth.User{ID: "user-b", Role: "user", Status: "active"}
	admin := &auth.User{ID: "admin-1", Role: "admin", Status: "active"}

	sessA := &store.SessionRecord{ID: "sess-a", UserID: "user-a"}
	sessB := &store.SessionRecord{ID: "sess-b", UserID: "user-b"}
	sessLegacy := &store.SessionRecord{ID: "sess-legacy", UserID: ""}

	st := newTestStore(sessA, sessB, sessLegacy)
	m := newMasterForAccessTest(t, st)

	// auth 未启用 → 返回全部
	all, err := m.ListAllSessions(context.Background())
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// auth 启用 + 未登录 → 错误
	_, err = m.ListAllSessions(auth.WithAuthEnabled(context.Background()))
	assert.Error(t, err)

	// user A → 只看到自己的，无主 session 不可见
	ctxA := auth.WithUser(auth.WithAuthEnabled(context.Background()), userA)
	listA, err := m.ListAllSessions(ctxA)
	require.NoError(t, err)
	ids := make(map[string]bool)
	for _, r := range listA {
		ids[r.ID] = true
	}
	assert.True(t, ids["sess-a"])
	assert.False(t, ids["sess-legacy"])
	assert.False(t, ids["sess-b"])

	// user B → 只看到自己的，无主 session 不可见
	ctxB := auth.WithUser(auth.WithAuthEnabled(context.Background()), userB)
	listB, err := m.ListAllSessions(ctxB)
	require.NoError(t, err)
	ids = make(map[string]bool)
	for _, r := range listB {
		ids[r.ID] = true
	}
	assert.True(t, ids["sess-b"])
	assert.False(t, ids["sess-legacy"])
	assert.False(t, ids["sess-a"])

	// admin → 只看到自己的，无主 session 也不可见
	ctxAdmin := auth.WithUser(auth.WithAuthEnabled(context.Background()), admin)
	listAdmin, err := m.ListAllSessions(ctxAdmin)
	require.NoError(t, err)
	assert.Len(t, listAdmin, 0)
}
