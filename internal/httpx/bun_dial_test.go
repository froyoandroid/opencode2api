package httpx

import (
	"bytes"
	"net"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

// TestBunClientHelloShape pins the structural fingerprint of the hello: the
// cipher list, the extension ids in wire order, and each extension's body
// length. The random and the key share vary per connection, so the assertion
// stops short of a byte comparison of those two fields.
func TestBunClientHelloShape(t *testing.T) {
	hello := captureHello(t, "opencode.ai")
	got := parseHello(t, hello)

	wantCiphers := []uint16{
		0x1301, 0x1302, 0x1303,
		0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8,
		0xc009, 0xc013, 0xc00a, 0xc014,
		0x009c, 0x009d, 0x002f, 0x0035,
	}
	if len(got.ciphers) != len(wantCiphers) {
		t.Fatalf("cipher count = %d, want %d", len(got.ciphers), len(wantCiphers))
	}
	for i, c := range wantCiphers {
		if got.ciphers[i] != c {
			t.Fatalf("cipher %d = %04x, want %04x", i, got.ciphers[i], c)
		}
	}

	wantExts := []uint16{
		0x0000, // server_name
		0x0017, // extended_master_secret
		0xff01, // renegotiation_info
		0x000a, // supported_groups
		0x000b, // ec_point_formats
		0x0023, // session_ticket
		0x0010, // ALPN
		0x0005, // status_request
		0x000d, // signature_algorithms
		0x0012, // signed_certificate_timestamp
		0x0033, // key_share
		0x002d, // psk_key_exchange_modes
		0x002b, // supported_versions
	}
	if len(got.exts) != len(wantExts) {
		t.Fatalf("extension count = %d, want %d", len(got.exts), len(wantExts))
	}
	for i, e := range wantExts {
		if got.exts[i] != e {
			t.Fatalf("extension %d = %04x, want %04x", i, got.exts[i], e)
		}
	}

	if len(got.sessionID) != 32 {
		t.Fatalf("session id length = %d, want 32", len(got.sessionID))
	}
	if v := got.supportedVersions; !bytes.Equal(v, []byte{0x04, 0x03, 0x04, 0x03, 0x03}) {
		t.Fatalf("supported_versions body = %x, want 0403040303", v)
	}
	if v := got.groups; !bytes.Equal(v, []byte{
		0x00, 0x08, 0x11, 0xec, 0x00, 0x1d, 0x00, 0x17, 0x00, 0x18,
	}) {
		t.Fatalf("supported_groups body = %x", v)
	}
	if !bytes.Equal(got.alpn, []byte{0x00, 0x09, 0x08, 0x68, 0x74, 0x74, 0x70, 0x2f, 0x31, 0x2e, 0x31}) {
		t.Fatalf("ALPN body = %x, want http/1.1", got.alpn)
	}
}

// TestBunClientHelloHandshakes proves the pinned spec still completes a TLS
// handshake against the real upstream. A hello that only serializes is not
// enough: a raw server_name extension or an unmodelled cipher leaves the
// fingerprint correct and the connection dead.
func TestBunClientHelloHandshakes(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "opencode.ai:443", 10*time.Second)
	if err != nil {
		t.Skipf("no route to opencode.ai: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	uconn := utls.UClient(conn, &utls.Config{ServerName: "opencode.ai"}, utls.HelloCustom)
	if err := uconn.ApplyPreset(bunClientHello()); err != nil {
		t.Fatalf("ApplyPreset: %v", err)
	}
	if err := uconn.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	state := uconn.ConnectionState()
	if state.Version != utls.VersionTLS13 {
		t.Fatalf("negotiated %04x, want TLS 1.3", state.Version)
	}
	if state.NegotiatedProtocol != "http/1.1" {
		t.Fatalf("ALPN = %q, want http/1.1", state.NegotiatedProtocol)
	}
}

// captureHello dials a throwaway listener with the fingerprint spec and
// returns the ClientHello bytes the spec emitted.
func captureHello(t *testing.T, serverName string) []byte {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	got := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 8192)
		n, _ := conn.Read(buf)
		got <- buf[:n]
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	uconn := utls.UClient(conn, &utls.Config{ServerName: serverName}, utls.HelloCustom)
	if err := uconn.ApplyPreset(bunClientHello()); err != nil {
		t.Fatalf("ApplyPreset: %v", err)
	}
	go func() { _ = uconn.Handshake() }()

	select {
	case hello := <-got:
		return hello
	case <-time.After(5 * time.Second):
		t.Fatal("no ClientHello captured")
		return nil
	}
}

// helloShape is the decoded view of a ClientHello the assertions need.
type helloShape struct {
	ciphers           []uint16
	exts              []uint16
	sessionID         []byte
	groups            []byte
	sigalgs           []byte
	alpn              []byte
	supportedVersions []byte
}

// parseHello decodes a TLS record carrying a single ClientHello.
func parseHello(t *testing.T, raw []byte) helloShape {
	t.Helper()
	if len(raw) < 6 || raw[0] != 0x16 {
		t.Fatalf("not a TLS handshake record: %x", raw[:min(len(raw), 8)])
	}
	body := raw[5:]
	if body[0] != 0x01 {
		t.Fatalf("not a ClientHello: %x", body[0])
	}
	p := 4 + 2 + 32 // handshake header, legacy_version, random
	sidLen := int(body[p])
	p++
	if p+sidLen > len(body) {
		t.Fatal("session id overruns the hello")
	}
	out := helloShape{sessionID: body[p : p+sidLen]}
	p += sidLen

	csLen := int(body[p])<<8 | int(body[p+1])
	p += 2
	for i := 0; i < csLen; i += 2 {
		out.ciphers = append(out.ciphers, uint16(body[p+i])<<8|uint16(body[p+i+1]))
	}
	p += csLen
	p += 1 + int(body[p]) // compression methods

	if p+2 > len(body) {
		t.Fatal("missing extensions block")
	}
	extLen := int(body[p])<<8 | int(body[p+1])
	p += 2
	end := p + extLen
	if end > len(body) {
		t.Fatal("extensions overrun the hello")
	}
	for p+4 <= end {
		id := uint16(body[p])<<8 | uint16(body[p+1])
		n := int(body[p+2])<<8 | int(body[p+3])
		p += 4
		if p+n > end {
			t.Fatalf("extension %04x overruns the block", id)
		}
		data := body[p : p+n]
		p += n
		out.exts = append(out.exts, id)
		switch id {
		case 0x000a:
			out.groups = data
		case 0x000d:
			out.sigalgs = data
		case 0x0010:
			out.alpn = data
		case 0x002b:
			out.supportedVersions = data
		}
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
