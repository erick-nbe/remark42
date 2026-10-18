package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	R "github.com/go-pkgz/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/remark42/backend/app/moderation"
	"github.com/umputun/remark42/backend/app/rest"
)

// postCommentAs posts a comment on the test server with the given JWT.
func postCommentAs(t *testing.T, ts *httptest.Server, token, text string) (*http.Response, []byte) {
	t.Helper()
	body := fmt.Sprintf(`{"text": %q, "locator":{"url": "https://radio-t.com/blah1", "site": "remark42"}}`, text)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/comment?site=remark42", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Add("X-JWT", token)
	client := http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	require.NoError(t, err)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return resp, b
}

func TestRest_ModerationBlockLinks(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		var err error
		srv.ModerationService, err = moderation.NewService(moderation.Config{BlockLinks: true}, nil)
		require.NoError(t, err)
	})
	defer teardown()

	t.Run("link rejected", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, devToken, "see [example](https://example.com)")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(body))
		c := R.JSON{}
		require.NoError(t, json.Unmarshal(body, &c))
		assert.Equal(t, float64(rest.ErrLinksBlocked), c["code"])
		assert.Equal(t, "links are not allowed in comments", c["error"])
	})

	t.Run("bare url rejected", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, devToken, "visit https://example.com now")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(body))
	})

	t.Run("url in code block allowed", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, devToken, "`https://example.com`")
		require.Equal(t, http.StatusCreated, resp.StatusCode, string(body),
			"a URL inside code is not a clickable link")
	})

	t.Run("image is not a link", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, devToken, "![pic](http://example.com/pic.png)")
		require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	})

	t.Run("admin can post links", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, adminUmputunToken, "see [example](https://example.com)")
		require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	})
}

func TestRest_ModerationMaxImages(t *testing.T) {
	ts, _, teardown := startupT(t, func(srv *Rest) {
		var err error
		srv.ModerationService, err = moderation.NewService(moderation.Config{MaxImages: 2}, nil)
		require.NoError(t, err)
	})
	defer teardown()

	t.Run("two images allowed", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, devToken,
			"![](http://example.com/1.png) ![](http://example.com/2.png)")
		require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
	})

	t.Run("three images rejected", func(t *testing.T) {
		resp, body := postCommentAs(t, ts, devToken,
			"![](http://example.com/1.png) ![](http://example.com/2.png) ![](http://example.com/3.png)")
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, string(body))
		c := R.JSON{}
		require.NoError(t, json.Unmarshal(body, &c))
		assert.Equal(t, float64(rest.ErrTooManyImages), c["code"])
		assert.Contains(t, c["error"], "too many images in comment")
	})
}

func TestRest_ModerationAccountAge(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ts, _, teardown := startupT(t, func(srv *Rest) {
		var err error
		srv.ModerationService, err = moderation.NewService(moderation.Config{
			MinAccountAgeForComment: 24 * time.Hour,
			Now:                     func() time.Time { return now },
		}, srv.DataService)
		require.NoError(t, err)
	})
	defer teardown()

	resp, body := postCommentAs(t, ts, devToken, "hello")
	require.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
	c := R.JSON{}
	require.NoError(t, json.Unmarshal(body, &c))
	assert.Equal(t, float64(rest.ErrAccountTooNew), c["code"])
	assert.Contains(t, c["error"], "account is too new to comment")

	now = now.Add(25 * time.Hour)
	resp, body = postCommentAs(t, ts, devToken, "hello again")
	require.Equal(t, http.StatusCreated, resp.StatusCode, string(body))
}

func TestRest_ModerationImageUploadAge(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	ts, _, teardown := startupT(t, func(srv *Rest) {
		var err error
		srv.ModerationService, err = moderation.NewService(moderation.Config{
			MinAccountAgeForImages: 48 * time.Hour,
			Now:                    func() time.Time { return now },
		}, srv.DataService)
		require.NoError(t, err)
	})
	defer teardown()

	upload := func() (*http.Response, []byte) {
		t.Helper()
		bodyBuf := &bytes.Buffer{}
		bodyWriter := multipart.NewWriter(bodyBuf)
		fileWriter, err := bodyWriter.CreateFormFile("file", "picture.png")
		require.NoError(t, err)
		_, err = io.Copy(fileWriter, gopherPNG())
		require.NoError(t, err)
		contentType := bodyWriter.FormDataContentType()
		require.NoError(t, bodyWriter.Close())

		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/picture?site=remark42", bodyBuf)
		require.NoError(t, err)
		req.Header.Add("Content-Type", contentType)
		req.Header.Add("X-JWT", devToken)
		client := http.Client{Timeout: 5 * time.Second}
		defer client.CloseIdleConnections()
		resp, err := client.Do(req)
		require.NoError(t, err)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		return resp, b
	}

	resp, body := upload()
	require.Equal(t, http.StatusForbidden, resp.StatusCode, string(body))
	c := R.JSON{}
	require.NoError(t, json.Unmarshal(body, &c))
	assert.Equal(t, float64(rest.ErrAccountTooNew), c["code"])
	assert.Contains(t, c["error"], "account is too new to attach images")

	now = now.Add(49 * time.Hour)
	resp, body = upload()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
}
