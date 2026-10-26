package gateway

import (
	"net/http"
	"net/url"
)

// relayTransport is an http.RoundTripper that routes every request through a
// header-based forward relay (such as a Vercel relay). The request is re-aimed
// at the relay URL and the original target is passed in the x-relay-target and
// x-relay-path headers, which the relay reads to forward the request and return
// the response. It wraps a base transport that dials the relay directly, so
// the relay (not this host) makes the final TLS connection to the upstream.
type relayTransport struct {
	base     http.RoundTripper
	relayURL *url.URL
}

// newRelayTransport returns a RoundTripper that relays requests through relay,
// delegating the relay-bound connection to base.
func newRelayTransport(base http.RoundTripper, relay *url.URL) *relayTransport {
	return &relayTransport{base: base, relayURL: relay}
}

// RoundTrip rewrites req to target the relay and forwards it via base. The
// body is preserved through req.Clone (GetBody survives), so the relay can
// re-send it to the target.
func (r *relayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target := req.URL.Scheme + "://" + req.URL.Host
	path := req.URL.RequestURI()

	relayPath := r.relayURL.Path
	req2 := req.Clone(req.Context())
	req2.URL = &url.URL{Scheme: r.relayURL.Scheme, Host: r.relayURL.Host}
	if relayPath != "" && relayPath != "/" {
		req2.URL.Path = relayPath
	}
	req2.Host = r.relayURL.Host
	req2.Header = req.Header.Clone()
	req2.Header.Set("x-relay-target", target)
	req2.Header.Set("x-relay-path", path)
	return r.base.RoundTrip(req2)
}
