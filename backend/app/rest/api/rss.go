package api

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"time"

	cache "github.com/go-pkgz/lcw/v2"
	log "github.com/go-pkgz/lgr"

	"github.com/umputun/remark42/backend/app/rest"
	"github.com/umputun/remark42/backend/app/store"
)

type rss struct {
	dataService rssStore
	cache       LoadingCache
}

type rssStore interface {
	Find(locator store.Locator, sort string, user store.User) ([]store.Comment, error)
	Last(siteID string, limit int, since time.Time, user store.User) ([]store.Comment, error)
	Get(locator store.Locator, commentID string, user store.User) (store.Comment, error)
	UserReplies(siteID, userID string, limit int, duration time.Duration) ([]store.Comment, string, error)
}

const maxRssItems = 20
const maxReplyDuration = 31 * 24 * time.Hour

// ui uses links like <post-url>#remark42__comment-<comment-id>
const uiNav = "#remark42__comment-"

// RssItemWithAvatar extends RssItem with author avatar support
type RssItemWithAvatar struct {
	XMLName      xml.Name `xml:"item"`
	Title        string   `xml:"title"`
	Link         string   `xml:"link"`
	Description  string   `xml:"description"`
	Author       string   `xml:"author,omitempty"`
	Guid         string   `xml:"guid"`
	PubDate      string   `xml:"pubDate,omitempty"`
	AuthorAvatar string   `xml:"authorAvatar,omitempty"`
}

// RssFeedWithAvatar represents RSS feed with avatar support
type RssFeedWithAvatar struct {
	XMLName     xml.Name `xml:"channel"`
	Title       string   `xml:"title"`
	Link        string   `xml:"link"`
	Description string   `xml:"description"`
	PubDate     string   `xml:"pubDate,omitempty"`
	Items       []*RssItemWithAvatar
}

// RssFeedXmlWithAvatar is the wrapper for RSS XML output
type RssFeedXmlWithAvatar struct {
	XMLName          xml.Name `xml:"rss"`
	Version          string   `xml:"version,attr"`
	ContentNamespace string   `xml:"xmlns:content,attr"`
	Channel          *RssFeedWithAvatar
}

// GET /rss/post?site=siteID&url=post-url
func (s *rss) postCommentsCtrl(w http.ResponseWriter, r *http.Request) {
	locator := store.Locator{SiteID: r.URL.Query().Get("site"), URL: r.URL.Query().Get("url")}
	log.Printf("[DEBUG] get rss for post %+v", locator)

	key := cache.NewKey(locator.SiteID).ID(URLKey(r)).Scopes(locator.SiteID, locator.URL)
	data, err := s.cache.Get(key, func() ([]byte, error) {
		comments, e := s.dataService.Find(locator, "-time", rest.GetUserOrEmpty(r))
		if e != nil {
			return nil, e
		}
		feed, e := s.toRssFeed(locator.URL, comments, "post comments for "+r.URL.Query().Get("url"))
		if e != nil {
			return nil, e
		}
		return []byte(feed), e
	})

	if err != nil {
		rest.SendErrorJSON(w, r, http.StatusBadRequest, err, "can't find comments", rest.ErrPostNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(data); err != nil {
		log.Printf("[WARN] failed to send response to %s, %s", r.RemoteAddr, err)
	}
}

// GET /rss/site?site=siteID
func (s *rss) siteCommentsCtrl(w http.ResponseWriter, r *http.Request) {
	siteID := r.URL.Query().Get("site")
	log.Printf("[DEBUG] get rss for site %s", siteID)

	key := cache.NewKey(siteID).ID(URLKey(r)).Scopes(siteID, lastCommentsScope)
	data, err := s.cache.Get(key, func() ([]byte, error) {
		comments, e := s.dataService.Last(siteID, maxRssItems, time.Time{}, rest.GetUserOrEmpty(r))
		if e != nil {
			return nil, e
		}

		feed, e := s.toRssFeed(r.URL.Query().Get("site"), comments, "site comment for "+siteID)
		if e != nil {
			return nil, e
		}
		return []byte(feed), e
	})

	if err != nil {
		rest.SendErrorJSON(w, r, http.StatusBadRequest, err, "can't get last comments", rest.ErrSiteNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(data); err != nil {
		log.Printf("[WARN] failed to send response to %s, %s", r.RemoteAddr, err)
	}
}

// GET /rss/reply?user=userID&site=siteID
func (s *rss) repliesCtrl(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user")
	siteID := r.URL.Query().Get("site")
	log.Printf("[DEBUG] get rss replies to user %s for site %s", userID, siteID)

	key := cache.NewKey(siteID).ID(URLKey(r)).Scopes(siteID, lastCommentsScope)
	data, err := s.cache.Get(key, func() (res []byte, e error) {
		replies, userName, e := s.dataService.UserReplies(siteID, userID, maxRssItems, maxReplyDuration)
		if e != nil {
			return nil, fmt.Errorf("can't get last comments: %w", e)
		}

		feed, e := s.toRssFeed(siteID, replies, "replies to "+userName)
		if e != nil {
			return nil, e
		}
		return []byte(feed), e
	})

	if err != nil {
		rest.SendErrorJSON(w, r, http.StatusBadRequest, err, "can't get replies", rest.ErrSiteNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(data); err != nil {
		log.Printf("[WARN] failed to send response to %s, %s", r.RemoteAddr, err)
	}
}

func (s *rss) toRssFeed(url string, comments []store.Comment, description string) (string, error) {
	if description == "" {
		description = "comment updates"
	}
	lastCommentTS := time.Unix(0, 0)
	if len(comments) > 0 {
		lastCommentTS = comments[0].Timestamp
	}

	feed := &RssFeedWithAvatar{
		Title:       "Remark42 comments",
		Link:        url,
		Description: description,
		PubDate:     lastCommentTS.Format(time.RFC1123Z),
		Items:       []*RssItemWithAvatar{},
	}

	for i, c := range comments {
		title := c.User.Name
		desc := c.Text
		if c.ParentID != "" {
			// add indication to parent comment
			parentComment, err := s.dataService.Get(c.Locator, c.ParentID, store.User{})
			if err == nil {
				title = fmt.Sprintf("%s > %s", c.User.Name, parentComment.User.Name)
				desc = desc + "<blockquote><p>" + parentComment.Snippet(300) + "</p></blockquote>"
			} else {
				log.Printf("[WARN] failed to get info about parent comment, %s", err)
			}
		}
		if c.PostTitle != "" {
			title = title + ", " + c.PostTitle
		}

		item := &RssItemWithAvatar{
			Title:        title,
			Link:         c.Locator.URL + uiNav + c.ID,
			Description:  desc,
			Author:       c.User.Name,
			Guid:         c.ID,
			PubDate:      c.Timestamp.Format(time.RFC1123Z),
			AuthorAvatar: c.User.Picture,
		}
		feed.Items = append(feed.Items, item)
		if i > maxRssItems {
			break
		}
	}

	feedXml := &RssFeedXmlWithAvatar{
		Version:          "2.0",
		ContentNamespace: "http://purl.org/rss/1.0/modules/content/",
		Channel:          feed,
	}

	data, err := xml.MarshalIndent(feedXml, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header[:len(xml.Header)-1] + string(data), nil
}
