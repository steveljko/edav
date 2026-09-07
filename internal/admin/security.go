package admin

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
)

type nonceKey struct{}

// nonceFrom returns the per-request script nonce.
func nonceFrom(ctx context.Context) string {
	nonce, _ := ctx.Value(nonceKey{}).(string)
	return nonce
}

// secureHeaders sets the policy for the admin interface.
//
// The theme is applied by an inline script before the first paint, which a
// policy without 'unsafe-inline' would otherwise block; a per-request nonce
// admits that one script and nothing else. Inline styles stay permitted
// because a collection's colour is user data rendered into a style attribute,
// and html/template escapes it for that context; an injected style is a far
// smaller problem than an injected script.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce, err := scriptNonce()
		if err != nil {
			http.Error(w, "something went wrong", http.StatusInternalServerError)
			return
		}

		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'nonce-"+nonce+"'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'; "+
				"base-uri 'none'")
		// frame-ancestors covers modern browsers; this covers the rest. Without
		// it an attacker's page can frame a delete confirmation and collect the
		// click.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), nonceKey{}, nonce)))
	})
}

// scriptNonce uses the URL-safe alphabet so the value survives into the markup
// unchanged. html/template rewrites "+" as "&#43;" in an attribute, which a
// browser decodes back but which makes the header and the page disagree on
// sight.
func scriptNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
