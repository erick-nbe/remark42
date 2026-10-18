// Package moderation implements account-age and content moderation rules applied by the
// REST layer before a comment or an image is stored.
//
// The account-age rules need a "first seen" timestamp per user. remark42 has no account
// creation date of its own (accounts belong to the auth providers) and the comments
// database is deliberately left untouched, so the timestamps live in a small JSON file
// named by Config.StateFile. Users who already have comments on the site are treated as
// established, so enabling the rules does not lock existing participants out.
package moderation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	log "github.com/go-pkgz/lgr"

	"github.com/umputun/remark42/backend/app/store"
)

var (
	// ErrAccountTooNew is returned when the user's account is younger than the configured minimum.
	ErrAccountTooNew = errors.New("account is too new")
	// ErrLinksForbidden is returned when a comment contains links and the block-links rule is on.
	ErrLinksForbidden = errors.New("links are not allowed in comments")
	// ErrTooManyImages is returned when a comment references more images than allowed.
	ErrTooManyImages = errors.New("too many images in comment")
)

// Config defines moderation rules. Zero durations and zero MaxImages disable the
// corresponding rule.
type Config struct {
	MinAccountAgeForComment time.Duration
	MinAccountAgeForImages  time.Duration
	BlockLinks              bool
	MaxImages               int
	StateFile               string
	Now                     func() time.Time // used by tests; time.Now when nil
}

// PriorComments lets the service recognize users who commented before the age rules
// were enabled.
type PriorComments interface {
	UserCount(siteID, userID string) (int, error)
}

// Service applies the configured moderation rules.
type Service struct {
	cfg       Config
	now       func() time.Time
	prior     PriorComments
	mu        sync.Mutex
	firstSeen map[string]time.Time
}

type state struct {
	Version   int                  `json:"version"`
	FirstSeen map[string]time.Time `json:"first_seen"`
}

const stateVersion = 1

// NewService builds a moderation service and loads the first-seen state if it exists.
func NewService(cfg Config, prior PriorComments) (*Service, error) {
	if cfg.MinAccountAgeForComment < 0 {
		return nil, fmt.Errorf("min account age for comment cannot be negative")
	}
	if cfg.MinAccountAgeForImages < 0 {
		return nil, fmt.Errorf("min account age for images cannot be negative")
	}
	if cfg.MaxImages < 0 {
		return nil, fmt.Errorf("max images cannot be negative")
	}

	s := &Service{
		cfg:       cfg,
		now:       cfg.Now,
		prior:     prior,
		firstSeen: map[string]time.Time{},
	}
	if s.now == nil {
		s.now = time.Now
	}

	if cfg.StateFile == "" {
		return s, nil
	}

	data, err := os.ReadFile(cfg.StateFile)
	switch {
	case err == nil:
		st := state{}
		if err = json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("can't parse moderation state file %s: %w", cfg.StateFile, err)
		}
		for k, v := range st.FirstSeen {
			s.firstSeen[k] = v
		}
	case errors.Is(err, os.ErrNotExist):
		// fresh state, nothing to load
	default:
		return nil, fmt.Errorf("can't read moderation state file %s: %w", cfg.StateFile, err)
	}
	return s, nil
}

// Enabled reports whether any rule is active.
func (s *Service) Enabled() bool {
	if s == nil {
		return false
	}
	return s.ageRulesEnabled() || s.cfg.BlockLinks || s.cfg.MaxImages > 0
}

// Track records the first time the user was seen on the site. It is called for authenticated
// requests, so the account-age clock starts at login rather than at the first comment.
func (s *Service) Track(siteID, userID string, admin bool) {
	if s == nil || admin || userID == "" || !s.ageRulesEnabled() {
		return
	}
	s.ensureSeen(siteID, userID)
}

// CheckNewComment applies all rules to a newly created comment.
func (s *Service) CheckNewComment(user store.User, siteID, commentHTML string) error {
	if s == nil || user.Admin {
		return nil
	}
	if s.cfg.MinAccountAgeForComment > 0 {
		if err := s.checkAge(siteID, user.ID, s.cfg.MinAccountAgeForComment, "comment"); err != nil {
			return err
		}
	}
	images, err := s.checkContent(commentHTML)
	if err != nil {
		return err
	}
	// images can also be attached by URL without going through the upload endpoint
	if s.cfg.MinAccountAgeForImages > 0 && images > 0 {
		return s.checkAge(siteID, user.ID, s.cfg.MinAccountAgeForImages, "attach images")
	}
	return nil
}

// CheckCommentUpdate applies content rules to an edited comment and the image-age rule
// when the edit adds images.
func (s *Service) CheckCommentUpdate(user store.User, siteID, newHTML, prevHTML string) error {
	if s == nil || user.Admin {
		return nil
	}
	images, err := s.checkContent(newHTML)
	if err != nil {
		return err
	}
	if s.cfg.MinAccountAgeForImages > 0 {
		_, prevImages, prevErr := analyze(prevHTML)
		if prevErr != nil {
			// the stored comment should always parse; counting no images keeps the age rule on
			log.Printf("[WARN] can't parse previous comment html: %v", prevErr)
		}
		if images > prevImages {
			return s.checkAge(siteID, user.ID, s.cfg.MinAccountAgeForImages, "attach images")
		}
	}
	return nil
}

// CheckImageUpload applies the image-age rule to a picture upload.
func (s *Service) CheckImageUpload(user store.User, siteID string) error {
	if s == nil || user.Admin || s.cfg.MinAccountAgeForImages <= 0 {
		return nil
	}
	return s.checkAge(siteID, user.ID, s.cfg.MinAccountAgeForImages, "attach images")
}

func (s *Service) ageRulesEnabled() bool {
	return s.cfg.MinAccountAgeForComment > 0 || s.cfg.MinAccountAgeForImages > 0
}

// checkContent applies the link and image-count rules and returns the number of images.
func (s *Service) checkContent(commentHTML string) (int, error) {
	links, images, err := analyze(commentHTML)
	if err != nil {
		return 0, err
	}
	if s.cfg.BlockLinks && links > 0 {
		return images, ErrLinksForbidden
	}
	if s.cfg.MaxImages > 0 && images > s.cfg.MaxImages {
		return images, fmt.Errorf("%w: maximum is %d, got %d", ErrTooManyImages, s.cfg.MaxImages, images)
	}
	return images, nil
}

func (s *Service) checkAge(siteID, userID string, minAge time.Duration, action string) error {
	first := s.ensureSeen(siteID, userID)
	if first.IsZero() { // user commented before the rules were enabled
		return nil
	}
	age := s.now().Sub(first)
	if age < minAge {
		return &AccountAgeError{Action: action, Required: minAge, Age: age}
	}
	return nil
}

// ensureSeen returns the timestamp the user was first seen, recording it when missing.
// A zero timestamp means the user is considered established because they already have
// comments on the site.
func (s *Service) ensureSeen(siteID, userID string) time.Time {
	if userID == "" {
		return time.Time{}
	}
	key := stateKey(siteID, userID)

	s.mu.Lock()
	first, ok := s.firstSeen[key]
	s.mu.Unlock()
	if ok {
		return first
	}

	seenAt := s.now()
	if s.prior != nil {
		if cnt, err := s.prior.UserCount(siteID, userID); err == nil && cnt > 0 {
			seenAt = time.Time{} // user commented before the rules existed
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if first, ok = s.firstSeen[key]; ok { // another request recorded it first
		return first
	}
	s.firstSeen[key] = seenAt
	s.saveLocked()
	return seenAt
}

// saveLocked persists the state. The caller must hold s.mu.
func (s *Service) saveLocked() {
	if s.cfg.StateFile == "" {
		return
	}
	data, err := json.Marshal(state{Version: stateVersion, FirstSeen: s.firstSeen})
	if err != nil {
		log.Printf("[WARN] can't marshal moderation state: %v", err)
		return
	}

	dir := filepath.Dir(s.cfg.StateFile)
	if dir != "." && dir != "" {
		if err = os.MkdirAll(dir, 0o700); err != nil {
			log.Printf("[WARN] can't create moderation state directory %s: %v", dir, err)
			return
		}
	}

	tmp := s.cfg.StateFile + ".tmp"
	if err = os.WriteFile(tmp, data, 0o600); err != nil {
		log.Printf("[WARN] can't write moderation state file %s: %v", tmp, err)
		return
	}
	if err = os.Rename(tmp, s.cfg.StateFile); err != nil {
		_ = os.Remove(s.cfg.StateFile) // Windows cannot rename over an existing file
		if err = os.Rename(tmp, s.cfg.StateFile); err != nil {
			log.Printf("[WARN] can't commit moderation state file %s: %v", s.cfg.StateFile, err)
		}
	}
}

// AccountAgeError is returned when the account is younger than the configured minimum.
type AccountAgeError struct {
	Action   string
	Required time.Duration
	Age      time.Duration
}

// Error implements the error interface.
func (e *AccountAgeError) Error() string {
	remaining := e.Required - e.Age
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("account is too new to %s: minimum account age is %s, try again in %s",
		e.Action, humanDuration(e.Required), remaining.Round(time.Second))
}

// Unwrap allows errors.Is(err, ErrAccountTooNew).
func (e *AccountAgeError) Unwrap() error { return ErrAccountTooNew }

func humanDuration(d time.Duration) string {
	if d >= 24*time.Hour && d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	return d.Round(time.Second).String()
}

func stateKey(siteID, userID string) string {
	return siteID + "\x00" + userID
}

// analyze parses the rendered comment HTML and returns the number of links and images.
func analyze(commentHTML string) (links, images int, err error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(commentHTML))
	if err != nil {
		return 0, 0, fmt.Errorf("can't parse comment html: %w", err)
	}
	return doc.Find("a[href]").Length(), doc.Find("img").Length(), nil
}
