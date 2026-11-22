// Package notionapi is the optional import straight from the Notion API. It is the one feature that talks to the
// network, and only ever to api.notion.com, only while an import runs, and only after the user turned it on in Settings.
//
// It reads everything the integration can see and writes it into an in-memory copy of a Notion export (a CSV per
// database and a Markdown file per page), so the normal importer, preview, merge strategies and undo all apply. The
// token lives in memory for the duration of the job and is never written to disk or logged.
package notionapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Base is the only host this package talks to. Version is the API version the data shapes were written against (data
// sources, not databases).
const (
	Base    = "https://api.notion.com/v1"
	Version = "2026-03-11"
)

// Code classifies a failure.
type Code string

const (
	BadToken    Code = "BAD_TOKEN"
	RateLimited Code = "RATE_LIMITED"
	Unavailable Code = "UNAVAILABLE"
	Cancelled   Code = "CANCELLED"
	Failed      Code = "FAILED"
)

// Error is a failure talking to Notion. Its message never contains the token.
type Error struct {
	Code    Code
	Message string
	Status  int
}

func (e *Error) Error() string { return e.Message }

func fail(code Code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Obj is a decoded JSON object. Notion's shapes are large and change; only the fields read here are looked at.
type Obj = map[string]any

// ClientOptions tune a Client. Zero values are the polite defaults.
type ClientOptions struct {
	// Do sends a request (default: http.DefaultClient.Do). Tests replace it with a fake Notion.
	Do func(*http.Request) (*http.Response, error)
	// MinInterval is the gap between requests: about three a second, Notion's average limit (default 350 ms).
	MinInterval time.Duration
	// Sleep waits (default: a timer that honours the context). Tests replace it to run instantly.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Client is a polite Notion client: it paces requests, honours Retry-After on 429 and retries transient errors.
type Client struct {
	token string
	o     ClientOptions
	last  time.Time
}

// NewClient makes a client. The token is kept only in this struct.
func NewClient(token string, o ClientOptions) *Client {
	if o.Do == nil {
		o.Do = http.DefaultClient.Do
	}
	if o.MinInterval == 0 {
		o.MinInterval = 350 * time.Millisecond
	}
	if o.Sleep == nil {
		o.Sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return &Client{token: token, o: o}
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if err := c.o.Sleep(ctx, d); err != nil {
		return fail(Cancelled, "Cancelled")
	}
	return nil
}

// Request calls the API and decodes the JSON answer into out.
func (c *Client) Request(ctx context.Context, method, path string, body any, out any) error {
	url := Base + path
	if !strings.HasPrefix(url, Base+"/") {
		return fail(Failed, "Bad URL")
	}
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fail(Failed, "Could not encode the request")
		}
	}
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return fail(Cancelled, "Cancelled")
		}
		if wait := c.o.MinInterval - time.Since(c.last); wait > 0 {
			if err := c.sleep(ctx, wait); err != nil {
				return err
			}
		}
		c.last = time.Now()
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
		if err != nil {
			return fail(Failed, "Bad request")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Notion-Version", Version)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := c.o.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return fail(Cancelled, "Cancelled")
			}
			if attempt < 3 {
				if err := c.sleep(ctx, time.Second<<attempt); err != nil {
					return err
				}
				continue
			}
			return fail(Unavailable, "Could not reach api.notion.com") // the underlying error is dropped on purpose
		}
		data, readErr := io.ReadAll(res.Body)
		_ = res.Body.Close()
		switch {
		case res.StatusCode >= 200 && res.StatusCode < 300:
			if readErr != nil {
				return fail(Unavailable, "Could not reach api.notion.com")
			}
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(data, out); err != nil {
				return fail(Failed, "Notion sent a response that could not be read")
			}
			return nil
		case res.StatusCode == 401:
			return fail(BadToken, "Notion rejected the token. Check it and try again.")
		case res.StatusCode == 429 && attempt < 6:
			wait := 2.0
			if v, err := strconv.ParseFloat(strings.TrimSpace(res.Header.Get("Retry-After")), 64); err == nil && v >= 0 && !math.IsInf(v, 0) {
				wait = v
			}
			if err := c.sleep(ctx, time.Duration(math.Min(60, wait)*float64(time.Second))); err != nil {
				return err
			}
			continue
		case res.StatusCode >= 500 && attempt < 3:
			if err := c.sleep(ctx, time.Second<<attempt); err != nil {
				return err
			}
			continue
		}
		code := Failed
		if res.StatusCode == 429 {
			code = RateLimited
		}
		return &Error{Code: code, Message: fmt.Sprintf("Notion returned %d", res.StatusCode), Status: res.StatusCode}
	}
}

// IsCode reports whether err is a notionapi Error with the code.
func IsCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}
