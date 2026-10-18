package moderation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/remark42/backend/app/store"
)

type mockPrior struct {
	count int
	err   error
}

func (m mockPrior) UserCount(string, string) (int, error) { return m.count, m.err }

func TestServiceContentRules(t *testing.T) {
	svc, err := NewService(Config{BlockLinks: true, MaxImages: 2}, nil)
	require.NoError(t, err)

	user := store.User{ID: "user1", Name: "user"}
	tests := []struct {
		name    string
		html    string
		wantErr error
	}{
		{name: "plain text", html: "<p>hello there</p>"},
		{name: "empty", html: ""},
		{name: "markdown link", html: `<p><a href="https://example.com">example</a></p>`, wantErr: ErrLinksForbidden},
		{name: "mailto link", html: `<p><a href="mailto:user@example.com">mail</a></p>`, wantErr: ErrLinksForbidden},
		{name: "raw html link", html: `<p>see <a href="http://example.com">this</a></p>`, wantErr: ErrLinksForbidden},
		{name: "image is not a link", html: `<p><img src="https://example.com/i.png" alt=""/></p>`},
		{name: "two images allowed", html: `<p><img src="a.png"/><img src="b.png"/></p>`},
		{name: "three images rejected", html: `<p><img src="a.png"/><img src="b.png"/><img src="c.png"/></p>`,
			wantErr: ErrTooManyImages},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.CheckNewComment(user, "site", tt.html)
			if tt.wantErr == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestServiceAdminBypass(t *testing.T) {
	now := time.Now()
	svc, err := NewService(Config{
		MinAccountAgeForComment: 24 * time.Hour,
		MinAccountAgeForImages:  24 * time.Hour,
		BlockLinks:              true,
		MaxImages:               1,
		Now:                     func() time.Time { return now },
	}, nil)
	require.NoError(t, err)

	admin := store.User{ID: "admin", Name: "admin", Admin: true}
	html := `<p><a href="https://example.com">link</a><img src="a"/><img src="b"/></p>`
	require.NoError(t, svc.CheckNewComment(admin, "site", html))
	require.NoError(t, svc.CheckImageUpload(admin, "site"))
	require.NoError(t, svc.CheckCommentUpdate(admin, "site", html, ""))
}

func TestServiceAccountAge(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	svc, err := NewService(Config{
		MinAccountAgeForComment: 24 * time.Hour,
		Now:                     func() time.Time { return now },
	}, nil)
	require.NoError(t, err)

	user := store.User{ID: "user1", Name: "user"}

	err = svc.CheckNewComment(user, "site", "<p>hello</p>")
	require.ErrorIs(t, err, ErrAccountTooNew)
	var ageErr *AccountAgeError
	require.ErrorAs(t, err, &ageErr)
	assert.Equal(t, "comment", ageErr.Action)
	assert.Equal(t, 24*time.Hour, ageErr.Required)
	assert.Equal(t, time.Duration(0), ageErr.Age)
	assert.Contains(t, ageErr.Error(), "minimum account age is 1 day")

	now = now.Add(23 * time.Hour)
	require.ErrorIs(t, svc.CheckNewComment(user, "site", "<p>hello</p>"), ErrAccountTooNew,
		"still too new one hour before the minimum")

	now = now.Add(2 * time.Hour)
	require.NoError(t, svc.CheckNewComment(user, "site", "<p>hello</p>"))

	// a different site keeps its own first-seen clock
	require.ErrorIs(t, svc.CheckNewComment(user, "other-site", "<p>hello</p>"), ErrAccountTooNew)
}

func TestServiceTrackStartsClock(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	svc, err := NewService(Config{
		MinAccountAgeForComment: 24 * time.Hour,
		Now:                     func() time.Time { return now },
	}, nil)
	require.NoError(t, err)

	user := store.User{ID: "user1", Name: "user"}
	svc.Track("site", user.ID, false) // e.g. GET /user right after login
	now = now.Add(25 * time.Hour)

	require.NoError(t, svc.CheckNewComment(user, "site", "<p>hello</p>"),
		"the clock started when the user was first seen, not at the first comment")
}

func TestServicePriorCommentsAreEstablished(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	user := store.User{ID: "user1", Name: "user"}

	t.Run("existing commenter is grandfathered", func(t *testing.T) {
		svc, err := NewService(Config{
			MinAccountAgeForComment: 24 * time.Hour,
			Now:                     func() time.Time { return now },
		}, mockPrior{count: 3})
		require.NoError(t, err)
		require.NoError(t, svc.CheckNewComment(user, "site", "<p>hello</p>"))
		require.NoError(t, svc.CheckImageUpload(user, "site"))
	})

	t.Run("user without comments starts the clock now", func(t *testing.T) {
		svc, err := NewService(Config{
			MinAccountAgeForComment: 24 * time.Hour,
			Now:                     func() time.Time { return now },
		}, mockPrior{err: errors.New("no comments")})
		require.NoError(t, err)
		require.ErrorIs(t, svc.CheckNewComment(user, "site", "<p>hello</p>"), ErrAccountTooNew)
	})
}

func TestServiceImageAge(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	svc, err := NewService(Config{
		MinAccountAgeForImages: 48 * time.Hour,
		Now:                    func() time.Time { return now },
	}, nil)
	require.NoError(t, err)

	user := store.User{ID: "user1", Name: "user"}
	require.ErrorIs(t, svc.CheckImageUpload(user, "site"), ErrAccountTooNew)
	require.ErrorIs(t, svc.CheckNewComment(user, "site", `<p><img src="a"/></p>`), ErrAccountTooNew,
		"images linked by URL are gated as well")
	require.NoError(t, svc.CheckNewComment(user, "site", "<p>text only</p>"),
		"the image rule does not block text-only comments")
	require.ErrorIs(t, svc.CheckCommentUpdate(user, "site", `<p><img src="a"/></p>`, "<p>text</p>"),
		ErrAccountTooNew, "editing a comment to add an image is gated too")

	now = now.Add(49 * time.Hour)
	require.NoError(t, svc.CheckImageUpload(user, "site"))
	require.NoError(t, svc.CheckNewComment(user, "site", `<p><img src="a"/></p>`))
	require.NoError(t, svc.CheckCommentUpdate(user, "site", `<p><img src="a"/></p>`, "<p>text</p>"))

	// keeping the existing image count is not adding images
	require.NoError(t, svc.CheckCommentUpdate(user, "site",
		`<p><img src="a"/> edited</p>`, `<p><img src="a"/></p>`))
}

func TestServiceStatePersistence(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "moderation", "state.json")
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	cfg := Config{
		MinAccountAgeForComment: 24 * time.Hour,
		StateFile:               stateFile,
		Now:                     func() time.Time { return now },
	}
	user := store.User{ID: "user1", Name: "user"}

	svc, err := NewService(cfg, nil)
	require.NoError(t, err)
	require.ErrorIs(t, svc.CheckNewComment(user, "site", "<p>hello</p>"), ErrAccountTooNew)

	_, err = os.Stat(stateFile)
	require.NoError(t, err, "first seen must be written to the state file")

	// a restart must not reset the clock
	now = now.Add(23 * time.Hour)
	restarted, err := NewService(cfg, nil)
	require.NoError(t, err)
	require.ErrorIs(t, restarted.CheckNewComment(user, "site", "<p>hello</p>"), ErrAccountTooNew)

	now = now.Add(2 * time.Hour)
	restarted, err = NewService(cfg, nil)
	require.NoError(t, err)
	require.NoError(t, restarted.CheckNewComment(user, "site", "<p>hello</p>"))
}

func TestNewServiceRejectsBadConfig(t *testing.T) {
	_, err := NewService(Config{MaxImages: -1}, nil)
	require.Error(t, err)

	_, err = NewService(Config{MinAccountAgeForComment: -time.Second}, nil)
	require.Error(t, err)

	_, err = NewService(Config{MinAccountAgeForImages: -time.Second}, nil)
	require.Error(t, err)

	_, err = NewService(Config{StateFile: filepath.Join(t.TempDir(), "bad.json")}, nil)
	require.NoError(t, err, "missing state file is a fresh state")

	badFile := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(badFile, []byte("{not json"), 0o600))
	_, err = NewService(Config{StateFile: badFile}, nil)
	require.Error(t, err)
}

func TestServiceDisabled(t *testing.T) {
	svc, err := NewService(Config{}, nil)
	require.NoError(t, err)
	assert.False(t, svc.Enabled())
	assert.NoError(t, svc.CheckNewComment(store.User{ID: "u"}, "site", `<a href="https://x">x</a>`))

	var nilSvc *Service
	assert.False(t, nilSvc.Enabled())
	assert.NoError(t, nilSvc.CheckNewComment(store.User{ID: "u"}, "site", "<a href=\"https://x\">x</a>"))
	assert.NoError(t, nilSvc.CheckImageUpload(store.User{ID: "u"}, "site"))
	assert.NoError(t, nilSvc.CheckCommentUpdate(store.User{ID: "u"}, "site", "", ""))
	nilSvc.Track("site", "u", false) // must not panic
}
