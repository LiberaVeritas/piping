package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"piping/internal/semester"
	"piping/internal/user"
)

type ClientConfig struct {
	RedirectURI  string
	ClientID     string
	ClientSecret string
	Scopes       string
	Sealer       Sealer
}

type Client struct {
	cfg              ClientConfig
	authEndpoint     string
	tokenEndpoint    string
	userInfoEndpoint string
	http             *http.Client
}

type oidcDiscovery struct {
	AuthEndpoint     string `json:"authorization_endpoint"`
	TokenEndpoint    string `json:"token_endpoint"`
	UserInfoEndpoint string `json:"userinfo_endpoint"`
}

type pkceChallenge struct {
	verifier  string
	method    string
	challenge string
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Scope       string `json:"scope"`
}

type State struct {
	OriginalURL  string `json:"original_url"`
	PKCEVerifier string `json:"pkce_verifier"`
	Scopes       string `json:"scopes"`
}

type UserInfo struct {
	Username string   `json:"sub"`
	FullName string   `json:"name"`
	Email    string   `json:"email"`
	Faculty  string   `json:"faculty"`
	Groups   []string `json:"groups"`
	Courses  []course `json:"courses"`
}

type course struct {
	Code     string `json:"code"`
	Section  string `json:"section"`
	Name     string `json:"name"`
	Semester string `json:"semester"`
}

type Sealer interface {
	SealAsJSON(label string, plainState any) (string, error)
	OpenAsJSON(label, sealedState string, plainState any) error
}

const stateLabel = "oidc_state"

func NewFromDiscovery(ctx context.Context, cfg ClientConfig, discoveryURL string) (*Client, error) {
	if cfg.Sealer == nil || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURI == "" || cfg.Scopes == "" {
		return nil, fmt.Errorf("Sealer is nil or missing client values client id %s, redirect uri %s, scopes %s",
			cfg.ClientID, cfg.RedirectURI, cfg.Scopes)
	}
	var d oidcDiscovery
	h := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discoveryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building discovery request: %w", err)
	}
	res, err := h.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching discovery %s: %w", discoveryURL, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("reading discovery response %s", res.Status)
	}
	err = json.UnmarshalRead(res.Body, &d)
	if err != nil {
		return nil, fmt.Errorf("reading discovery json %w", err)
	}
	if d.AuthEndpoint == "" || d.TokenEndpoint == "" || d.UserInfoEndpoint == "" {
		return nil, fmt.Errorf("missing endpoints from discovery %+v", d)
	}

	return &Client{
		cfg:              cfg,
		authEndpoint:     d.AuthEndpoint,
		tokenEndpoint:    d.TokenEndpoint,
		userInfoEndpoint: d.UserInfoEndpoint,
		http:             h,
	}, nil
}

func (c *Client) GetAuthURL(originalURL string) (string, error) {
	pkce := newPKCEChallenge()
	state := State{
		OriginalURL:  originalURL,
		PKCEVerifier: pkce.verifier,
		Scopes:       c.cfg.Scopes,
	}
	sealedState, err := c.cfg.Sealer.SealAsJSON(stateLabel, state)
	if err != nil {
		return "", fmt.Errorf("sealing state %s, %s: %w", stateLabel, state.OriginalURL, err)
	}

	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.cfg.ClientID},
		"scope":                 {c.cfg.Scopes},
		"redirect_uri":          {c.cfg.RedirectURI},
		"code_challenge":        {pkce.challenge},
		"code_challenge_method": {pkce.method},
		"state":                 {sealedState},
	}

	authURL, err := url.Parse(c.authEndpoint)
	if err != nil {
		return "", fmt.Errorf("parsing auth endpoint %s: %w", c.authEndpoint, err)
	}
	authURL.RawQuery = query.Encode()

	return authURL.String(), nil
}

func (c *Client) GetUserInfo(ctx context.Context, code, state string) (UserInfo, string, error) {
	var s State
	err := c.cfg.Sealer.OpenAsJSON(stateLabel, state, &s)
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("unsealing state %s %+v: %w", stateLabel, state, err)
	}

	formData := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURI},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code_verifier": {s.PKCEVerifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint,
		strings.NewReader(formData.Encode()))
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("creating request with %+v: %w", formData, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.http.Do(req)
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("posting to token endpoint: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return UserInfo{}, "", fmt.Errorf("reading token response %s", res.Status)
	}

	var token tokenResponse
	err = json.UnmarshalRead(res.Body, &token)
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("decoding token %+v: %w", token, err)
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodGet, c.userInfoEndpoint, nil)
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	res, err = c.http.Do(req)
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("exchanging token: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return UserInfo{}, "", fmt.Errorf("reading user info response %s", res.Status)
	}

	var userInfo UserInfo
	err = json.UnmarshalRead(res.Body, &userInfo)
	if err != nil {
		return UserInfo{}, "", fmt.Errorf("decoding user info %+v: %w", res, err)
	}

	return userInfo, s.OriginalURL, nil
}

func newPKCEChallenge() pkceChallenge {
	var p pkceChallenge
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	p.verifier = base64.RawURLEncoding.EncodeToString(key)
	checksum := sha256.Sum256([]byte(p.verifier))
	p.challenge = base64.RawURLEncoding.EncodeToString(checksum[:])
	p.method = "S256"
	return p
}

func firstRDN(dn string) (string, bool) {
	parsed, err := ldap.ParseDN(dn)
	if err != nil || len(parsed.RDNs) == 0 || len(parsed.RDNs[0].Attributes) == 0 {
		return "", false
	}
	return parsed.RDNs[0].Attributes[0].Value, true
}

func (u UserInfo) ToUser() (user.User, error) {
	groups := []string{}
	for _, dn := range u.Groups {
		group, ok := firstRDN(dn)
		if ok {
			groups = append(groups, group)
		}
	}
	enrolled := map[int]bool{}
	for _, course := range u.Courses {
		code, err := semester.Code(course.Semester)
		if err != nil {
			continue
			//return user.User{}, fmt.Errorf("failed to parse course %s: %w", course, err)
		}
		enrolled[code] = true
	}
	enrolments := make([]int, 0, len(enrolled))
	for code := range enrolled {
		enrolments = append(enrolments, code)
	}
	role := user.RoleFromGroups(groups)
	eligible := user.EligibleForQuota(u.Faculty, groups)

	return user.User{
		Username:      u.Username,
		FullName:      u.FullName,
		Email:         u.Email,
		Role:          role,
		QuotaEligible: eligible,
		Enrolments:    enrolments,
	}, nil
}
