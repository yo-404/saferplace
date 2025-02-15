// Copyright 2023 SaferPlace

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"safer.place/internal/database"
)

var (
	ErrBadFormat = errors.New("authorization not in correct Bearer: $token format")
)

type githubTokenResponse struct {
	AccessToken string `json:"access_token"`
}

// Configure the authentication. For now we just use Github
// but if needed this can be expanded.
type Config struct {
	Handler      http.Handler
	Log          *zap.Logger
	Domain       string
	ClientID     string
	ClientSecret string
	DB           database.Database
}

type Auth struct {
	handler     http.Handler
	prefix      string
	callbackURL string
	mux         *http.ServeMux
	cfg         *Config
	client      *http.Client
	log         *zap.Logger
	db          database.Database
}

// Register the
func Register(prefix string, cfg *Config) func() (string, http.Handler) {
	a := &Auth{
		cfg:     cfg,
		handler: cfg.Handler,
		mux:     http.NewServeMux(),
		callbackURL: fmt.Sprintf(
			"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s",
			cfg.ClientID,
			fmt.Sprintf(
				"%s%soauth/callback",
				cfg.Domain,
				prefix,
			),
		),
		prefix: prefix,
		client: http.DefaultClient,
		log:    cfg.Log,
		db:     cfg.DB,
	}
	a.mux.HandleFunc("/oauth/callback", a.callback)
	a.mux.HandleFunc("/", a.index)

	cfg.Log.Info("authentication set up",
		zap.String("prefix", prefix),
		zap.String("callback", a.callbackURL),
	)

	return func() (string, http.Handler) {
		return prefix, http.StripPrefix(strings.TrimRight(prefix, "/"), a.mux)
	}
}

func (a *Auth) index(w http.ResponseWriter, r *http.Request) {
	if authenticated, err := a.authenticated(r); err != nil || !authenticated {
		if err != nil {
			http.Error(w, fmt.Sprintf("unable to authenticate: %v", err), http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, a.callbackURL, http.StatusTemporaryRedirect)
		return
	}
	a.handler.ServeHTTP(w, r)
}

func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	a.log.Info("callback")
	code := r.URL.Query().Get("code")

	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	requestData, _ := json.Marshal(map[string]string{
		"client_id":     a.cfg.ClientID,
		"client_secret": a.cfg.ClientSecret,
		"code":          code,
	})

	req, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		"https://github.com/login/oauth/access_token",
		bytes.NewBuffer(requestData),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	a.log.Info("sending the request to github to validate code")

	resp, err := a.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	a.log.Info("request validated")

	var tokenData githubTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenData); err != nil {
		resp.Body.Close()
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	resp.Body.Close()

	if err := a.db.SaveSession(r.Context(), tokenData.AccessToken); err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "Authorization",
		Value:    "Bearer " + tokenData.AccessToken,
		MaxAge:   3600,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
	})

	http.Redirect(w, r, a.prefix, http.StatusTemporaryRedirect)
}

func (a *Auth) authenticated(r *http.Request) (bool, error) {
	cookie, err := r.Cookie("Authorization")
	if err != nil {
		a.log.Info("cookie not found")
		return false, nil
	}

	a.log.Info("checking if cookie", zap.Any("cookie", cookie))

	bearerToken := strings.Split(cookie.Value, " ")
	if len(bearerToken) != 2 {
		a.log.Info("not in 2 parts")
		return false, ErrBadFormat
	}

	if bearerToken[0] != "Bearer" {
		a.log.Info("bad format")
		return false, ErrBadFormat
	}

	session := bearerToken[1]

	if err := a.db.IsValidSession(r.Context(), session); err != nil {
		a.log.Error("unable to authenticate", zap.String("session", session), zap.Error(err))
		return false, nil
	}

	return true, nil
}
