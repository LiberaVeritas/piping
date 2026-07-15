package directory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"piping/internal/user"
)

var ErrUserNotFound = errors.New("user not found in active directory")

type Client struct {
	BaseURL        string
	NinshouPath    string
	AttrispoolPath string
	User           string
	Password       string
	HTTP           *http.Client
	Log            *slog.Logger
}

func New(baseURL, ninshouPath, attrispoolPath, user, password string, log *slog.Logger) *Client {
	return &Client{
		BaseURL:        baseURL,
		NinshouPath:    ninshouPath,
		AttrispoolPath: attrispoolPath,
		User:           user,
		Password:       password,
		HTTP:           &http.Client{Timeout: 10 * time.Second},
		Log:            log,
	}
}

type userEntry struct {
	User      string   `json:"short-user"`
	FirstName string   `json:"first-name"`
	LastName  string   `json:"last-name"`
	Email     string   `json:"email"`
	Faculty   string   `json:"faculty"`
	Groups    []string `json:"groups"`
	Courses   []struct {
		Name     string `json:"name"`
		Semester string `json:"semester"`
	} `json:"courses"`
}

func (c *Client) Lookup(ctx context.Context, username string) (user.User, error) {
	token, err := c.authenticate(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("authenticating with ninshou: %w", err)
	}
	return c.fetch(ctx, token, username)
}

func (c *Client) authenticate(ctx context.Context) (string, error) {
	body, err := json.Marshal(map[string]string{"user": c.User, "password": c.Password})
	if err != nil {
		return "", err
	}
	path, err := url.JoinPath(c.BaseURL, c.NinshouPath)
	if err != nil {
		return "", fmt.Errorf("building ninshou path: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ninshou returned %d for user %q", res.StatusCode, c.User)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 8192))
	if err != nil {
		return "", fmt.Errorf("reading ninshou token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("ninshou returned an empty token")
	}
	return token, nil
}

func (c *Client) fetch(ctx context.Context, token, username string) (user.User, error) {
	path, err := url.JoinPath(c.BaseURL, c.AttrispoolPath, url.PathEscape(username))
	if err != nil {
		return user.User{}, fmt.Errorf("building attrispool path: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return user.User{}, fmt.Errorf("building attrispool request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	res, err := c.HTTP.Do(req)
	if err != nil {
		return user.User{}, fmt.Errorf("attrispool request for %q: %w", username, err)
	}
	defer res.Body.Close()

	switch res.StatusCode {
	case http.StatusOK:
		u, err := decodeUser(res.Body, username)
		if err != nil {
			return user.User{}, err
		}
		return c.parseUserEntry(u)
	case http.StatusNotFound:
		return user.User{}, fmt.Errorf("looking up %q: %w", username, ErrUserNotFound)
	case http.StatusUnauthorized, http.StatusForbidden:
		return user.User{}, fmt.Errorf(
			"attrispool rejected the token (%d) for %q: is %q in attrispool's lookup allowlist, "+
				"and do ninshou/attrispool share a keypair?", res.StatusCode, username, c.User)
	default:
		body, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return user.User{}, fmt.Errorf("attrispool returned %d for %q: %s",
			res.StatusCode, username, strings.TrimSpace(string(body)))
	}
}

func decodeUser(r io.Reader, username string) (userEntry, error) {
	var u userEntry
	err := json.NewDecoder(r).Decode(&u)
	if err != nil {
		return userEntry{}, fmt.Errorf("decoding attrispool response for %q: %w", username, err)
	}
	if u.User == "" {
		return userEntry{}, fmt.Errorf("attrispool returned no user for %q: %w", username, ErrUserNotFound)
	}
	return u, nil
}

func (c *Client) parseUserEntry(entry userEntry) (user.User, error) {
	var sems []int
	seen := map[int]bool{}
	for _, course := range entry.Courses {
		code, err := SemesterCode(course.Semester)
		if err != nil {
			if c.Log != nil {
				c.Log.Warn("directory: unparseable course semester", "user", entry.User, "semester", course.Semester, "err", err)
			}
			continue
		}
		if !seen[code] {
			seen[code] = true
			sems = append(sems, code)
		}
	}
	return user.User{
		Username:  entry.User,
		FullName:  entry.FirstName + " " + entry.LastName,
		Email:     entry.Email,
		Faculty:   entry.Faculty,
		Groups:    entry.Groups,
		Semesters: sems,
	}, nil
}

func SemesterCode(s string) (int, error) {
	season, year, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return 0, fmt.Errorf("parsing semester %s", s)
	}
	var month int
	switch season {
	case "Winter":
		month = 1
	case "Summer":
		month = 5
	case "Fall":
		month = 9
	default:
		return 0, fmt.Errorf("unknown season %q", season)
	}
	y, err := strconv.Atoi(year)
	if err != nil {
		return 0, fmt.Errorf("converting to string %s: %w", year, err)
	}
	return y*100 + month, nil
}
