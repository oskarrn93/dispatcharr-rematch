package dispatcharr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientPaginatesAndReauthenticates(t *testing.T) {
	logins, streamCalls := 0, 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/accounts/auth/login/", func(w http.ResponseWriter, _ *http.Request) {
		logins++
		_ = json.NewEncoder(w).Encode(map[string]string{"access": fmt.Sprintf("token-%d", logins)})
	})
	mux.HandleFunc("GET /api/channels/streams/", func(w http.ResponseWriter, r *http.Request) {
		streamCalls++
		// The first token "expires" on the first call.
		if r.Header.Get("Authorization") == "Bearer token-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("page") == "2" {
			_ = json.NewEncoder(w).Encode(map[string]any{"next": nil, "results": []Stream{{ID: 2, Name: "b"}}})
			return
		}
		// Dispatcharr builds `next` from its own host, which may not be ours.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"next":    "http://dispatcharr.invalid:9191/api/channels/streams/?page=2&page_size=500",
			"results": []Stream{{ID: 1, Name: "a", IsStale: true}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c, err := New(context.Background(), srv.URL+"/", "u", "p")
	require.NoError(t, err)
	streams, err := c.ListStreams(context.Background())
	require.NoError(t, err)

	assert.Equal(t, []Stream{{ID: 1, Name: "a", IsStale: true}, {ID: 2, Name: "b"}}, streams)
	assert.Equal(t, 2, logins)
	assert.Equal(t, 3, streamCalls)
}

func TestListActiveM3UAccountsFiltersInactive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]string{"access": "t"})
			return
		}
		_, _ = w.Write([]byte(`[{"id":1,"name":"on","is_active":true,"status":"success"},{"id":2,"name":"off","is_active":false}]`))
	}))
	defer srv.Close()

	c, err := New(context.Background(), srv.URL, "u", "p")
	require.NoError(t, err)
	accounts, err := c.ListActiveM3UAccounts(context.Background())
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	assert.Equal(t, "on", accounts[0].Name)
}
