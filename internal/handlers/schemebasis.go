package handlers

import (
	"encoding/json"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// How an admin sign-in's HTTPS was established, recorded on the admin
// device token (plan amendment A1) and shown per phone on the web page.
// "" is no evidence of HTTPS: a header said http, or nothing named a
// scheme and the sign-in did not ask to stay signed in.
const (
	// SchemeBasisTLS: this server terminated TLS itself.
	SchemeBasisTLS = "tls"
	// SchemeBasisProxy: a reverse proxy said so in X-Forwarded-Proto,
	// X-Forwarded-Protocol, X-Forwarded-Ssl: on or X-Url-Scheme — the
	// headers Fiber's c.Protocol() reads.
	SchemeBasisProxy = "proxy"
	// SchemeBasisForwarded: RFC 7239, Forwarded: proto=https.
	SchemeBasisForwarded = "forwarded"
	// SchemeBasisCloudflare: CF-Visitor: {"scheme":"https"}.
	SchemeBasisCloudflare = "cloudflare"
	// SchemeBasisConfig: ALEXANDRIA_PUBLIC_URL is an https:// address,
	// the operator's declaration that this server is only reached over
	// HTTPS. It vouches only for a sign-in that asked to stay signed in
	// and whose headers name no scheme.
	SchemeBasisConfig = "config"
	// SchemeBasisClient: only the phone's own declaration — it asked to
	// stay signed in, which it does only over a certificate-validated
	// https:// address — with no header naming a scheme and no operator
	// declaration.
	SchemeBasisClient = "client"
)

// schemeBasis records how HTTPS was established for an admin sign-in
// (see decideSchemeBasis for the rules). staySignedIn is the request's
// "persistent": the phone asks for it only over https.
//
// None of these headers is authenticated: trusted-proxy checking is off,
// so any client can send any of them. This is a record for the owner,
// not a gate — the phone decides persistence (amendment A1), and a
// client that can sign in already holds the password and a fresh code.
func schemeBasis(c *fiber.Ctx, httpsDeclared, staySignedIn bool) string {
	return decideSchemeBasis(
		c.Context().IsTLS(),
		proxyScheme(c),
		c.Get("Forwarded"),
		c.Get("CF-Visitor"),
		httpsDeclared,
		staySignedIn,
	)
}

// proxyHeaders are the headers Fiber's c.Protocol() reads.
var proxyHeaders = []string{
	fiber.HeaderXForwardedProto,
	fiber.HeaderXForwardedProtocol,
	fiber.HeaderXForwardedSsl,
	fiber.HeaderXUrlScheme,
}

// proxyScheme is the scheme the X-Forwarded-* headers give, as
// c.Protocol() reads them, or "" when the request carries none of them.
// c.Protocol() alone cannot tell the two apart: it answers "http" both
// for a proxy that said http and for no proxy at all.
func proxyScheme(c *fiber.Ctx) string {
	if !c.IsProxyTrusted() {
		return ""
	}
	for _, h := range proxyHeaders {
		if c.Get(h) != "" {
			return c.Protocol()
		}
	}
	return ""
}

// decideSchemeBasis is schemeBasis without the request, so every branch
// can be tested — direct TLS included, which an in-memory test request
// cannot carry. proxy is proxyScheme's answer; forwarded and cfVisitor
// are the raw headers. The rules, in order:
//
//  1. Cloudflare's CF-Visitor saying http: plain HTTP. Only Cloudflare's
//     edge sets that header, and the edge is the hop the client itself
//     connected to, so its http outranks an https from any hop further
//     in: Cloudflare can reach an HTTPS origin for a visitor who came in
//     over plain HTTP.
//  2. Evidence of HTTPS, most direct first: this server's own TLS, then
//     the X-Forwarded-* headers, RFC 7239 Forwarded, CF-Visitor. An https
//     outranks an http from another header, because the hops between a
//     proxy and this server are plain HTTP in every proxy setup: behind
//     a Cloudflare Tunnel, Nginx Proxy Manager forwards cloudflared's
//     plain-HTTP hop as X-Forwarded-Proto: http while CF-Visitor says
//     https.
//  3. A header saying http, with none saying https: plain HTTP. A header
//     is the request's own word on its scheme, and no declaration
//     overrules it.
//  4. No header names a scheme. Then a declaration fills in, but only
//     for a sign-in that asked to stay signed in, which the phone does
//     only over a certificate-validated https:// address: the operator's
//     (config), else the phone's own (client). A sign-in that did not
//     ask records nothing: the phone on a plain http:// address, or on
//     an https:// one where it could not keep a sign-in (no passcode),
//     and nothing here tells the two apart.
func decideSchemeBasis(
	isTLS bool,
	proxy, forwarded, cfVisitor string,
	httpsDeclared, staySignedIn bool,
) string {
	fwd := forwardedProto(forwarded)
	cf := cfVisitorScheme(cfVisitor)
	switch {
	case named(cf) && !isHTTPS(cf):
		return ""
	case isTLS:
		return SchemeBasisTLS
	case isHTTPS(proxy):
		return SchemeBasisProxy
	case isHTTPS(fwd):
		return SchemeBasisForwarded
	case isHTTPS(cf):
		return SchemeBasisCloudflare
	case named(proxy) || named(fwd):
		return ""
	case !staySignedIn:
		return ""
	case httpsDeclared:
		return SchemeBasisConfig
	}
	return SchemeBasisClient
}

// named reports whether a header named a scheme at all.
func named(scheme string) bool {
	return strings.TrimSpace(scheme) != ""
}

func isHTTPS(scheme string) bool {
	return strings.EqualFold(strings.TrimSpace(scheme), "https")
}

// forwardedProto is the proto of the first element of an RFC 7239
// Forwarded header that names one. The first element is the hop nearest
// the client, which is the one that saw the client's scheme.
func forwardedProto(header string) string {
	for _, element := range strings.Split(header, ",") {
		for _, pair := range strings.Split(element, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "proto") {
				return strings.Trim(strings.TrimSpace(value), `"`)
			}
		}
	}
	return ""
}

// cfVisitorScheme reads Cloudflare's CF-Visitor header,
// {"scheme":"https"}.
func cfVisitorScheme(header string) string {
	var v struct {
		Scheme string `json:"scheme"`
	}
	if header == "" || json.Unmarshal([]byte(header), &v) != nil {
		return ""
	}
	return v.Scheme
}

// schemeBasisForLog spells the basis for a log line.
func schemeBasisForLog(basis string) string {
	if basis == "" {
		return "none"
	}
	return basis
}
