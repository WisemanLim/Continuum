package api

import (
	"crypto/tls"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	ber "github.com/go-asn1-ber/asn1-ber"
	"github.com/go-ldap/ldap/v3"

	"continuum/internal/auth"
	"continuum/internal/pki"
)

// fakeLDAP is a tiny directory: simple bind, subtree search by a filter template, StartTLS refused.
type fakeLDAP struct {
	ln      net.Listener
	mu      sync.Mutex
	users   map[string]fakeEntry // uid → entry
	service [2]string            // bind DN, password
	filters []string
}

type fakeEntry struct {
	dn, password string
	groups       []string
}

func newFakeLDAP(t *testing.T, tc *tls.Config) *fakeLDAP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if tc != nil { // ldaps://
		ln = tls.NewListener(ln, tc)
	}
	f := &fakeLDAP{ln: ln, users: map[string]fakeEntry{}, service: [2]string{"cn=svc,dc=example,dc=test", "placeholder-svc"}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func ldapResult(id int64, app ber.Tag, code int64, msg string) *ber.Packet {
	p := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
	p.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, ""))
	op := ber.Encode(ber.ClassApplication, ber.TypeConstructed, app, nil, "")
	op.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagEnumerated, code, ""))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "", ""))
	op.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, msg, ""))
	p.AppendChild(op)
	return p
}

func (f *fakeLDAP) serve(c net.Conn) {
	defer c.Close()
	for {
		p, err := ber.ReadPacket(c)
		if err != nil || len(p.Children) < 2 {
			return
		}
		id := p.Children[0].Value.(int64)
		op := p.Children[1]
		switch op.Tag {
		case ldap.ApplicationBindRequest:
			dn := op.Children[1].Value.(string)
			pw := op.Children[2].Data.String()
			ok := dn == f.service[0] && pw == f.service[1]
			f.mu.Lock()
			for _, u := range f.users {
				ok = ok || (u.dn == dn && u.password == pw)
			}
			f.mu.Unlock()
			code := int64(ldap.LDAPResultInvalidCredentials)
			if ok {
				code = ldap.LDAPResultSuccess
			}
			c.Write(ldapResult(id, ldap.ApplicationBindResponse, code, "").Bytes())
		case ldap.ApplicationSearchRequest:
			filter, _ := ldap.DecompileFilter(op.Children[6])
			f.mu.Lock()
			f.filters = append(f.filters, filter)
			for uid, u := range f.users {
				if !strings.Contains(filter, "(uid="+uid+")") {
					continue
				}
				e := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
				e.AppendChild(ber.NewInteger(ber.ClassUniversal, ber.TypePrimitive, ber.TagInteger, id, ""))
				entry := ber.Encode(ber.ClassApplication, ber.TypeConstructed, ldap.ApplicationSearchResultEntry, nil, "")
				entry.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, u.dn, ""))
				attrs := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
				a := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSequence, nil, "")
				a.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, "memberOf", ""))
				vals := ber.Encode(ber.ClassUniversal, ber.TypeConstructed, ber.TagSet, nil, "")
				for _, g := range u.groups {
					vals.AppendChild(ber.NewString(ber.ClassUniversal, ber.TypePrimitive, ber.TagOctetString, g, ""))
				}
				a.AppendChild(vals)
				attrs.AppendChild(a)
				entry.AppendChild(attrs)
				e.AppendChild(entry)
				c.Write(e.Bytes())
			}
			f.mu.Unlock()
			c.Write(ldapResult(id, ldap.ApplicationSearchResultDone, ldap.LDAPResultSuccess, "").Bytes())
		case ldap.ApplicationExtendedRequest: // StartTLS: not offered
			c.Write(ldapResult(id, ldap.ApplicationExtendedResponse, ldap.LDAPResultProtocolError, "StartTLS not supported").Bytes())
		case ldap.ApplicationUnbindRequest:
			return
		}
	}
}

// E33: directory sign-in — service bind, filter escaping, user bind, group → role (re-evaluated every login),
// no fallthrough for local accounts, empty-password bind refused, StartTLS required unless disabled, MFA kept.
func TestLDAPLogin(t *testing.T) {
	h, login := authHarness(t)
	h.tok = tokenOf(login("admin", "0123456789", ""))
	dir := newFakeLDAP(t, nil)
	const groups = "ou=groups,dc=example,dc=test"
	dir.users["alice"] = fakeEntry{dn: "uid=alice,ou=people,dc=example,dc=test", password: "alice-pw", groups: []string{"cn=storage," + groups}}
	dir.users["bob"] = fakeEntry{dn: "uid=bob,ou=people,dc=example,dc=test", password: "bob-pw", groups: []string{"cn=nobody," + groups}}
	dir.users["admin"] = fakeEntry{dn: "uid=admin,ou=people,dc=example,dc=test", password: "directory-pw"}

	set := func(kv map[string]string) int { return h.do("PUT", "/v1/settings", kv, nil) }
	if st := set(map[string]string{"ldap.role_map": "cn=x=superuser"}); st != 400 {
		t.Fatalf("bad role in role_map: %d", st)
	}
	if st := set(map[string]string{"ldap.user_filter": "(uid=*)"}); st != 400 {
		t.Fatalf("filter without %%s: %d", st)
	}
	var out map[string]string
	if st := h.do("PUT", "/v1/settings", map[string]string{"ldap.url": "ldap://" + dir.ln.Addr().String(), "ldap.bind_dn": dir.service[0],
		"ldap.bind_password": dir.service[1], "ldap.user_base": "ou=people,dc=example,dc=test",
		"ldap.role_map": "cn=storage," + groups + "=storage-admin;cn=ops," + groups + "=operator", "ldap.starttls": "true"}, &out); st != 200 || out["ldap.bind_password"] != masked {
		t.Fatalf("settings: %d %v", st, out)
	}
	var status map[string]any
	h.do("GET", "/v1/auth/status", nil, &status)
	if status["ldap"] != true {
		t.Fatalf("status: %v", status)
	}
	// StartTLS on by default: a directory without it is "unavailable", not a silent plaintext bind
	if st, o := login("alice", "alice-pw", ""); st != 409 || !strings.Contains(o["error"].(map[string]any)["message"].(string), "StartTLS") {
		t.Fatalf("starttls refused: %d %v", st, o)
	}
	set(map[string]string{"ldap.starttls": "false"})

	st, o := login("alice", "alice-pw", "")
	alice := tokenOf(st, o)
	if u := o["user"].(map[string]any); u["role"] != "storage-admin" || u["auth_source"] != "ldap" {
		t.Fatalf("alice: %v", u)
	}
	if st, _ := login("alice", "wrong", ""); st != 403 {
		t.Fatalf("wrong password: %d", st)
	}
	if st, _ := login("alice", "", ""); st != 403 {
		t.Fatalf("empty password (unauthenticated bind) must be refused: %d", st)
	}
	if st, _ := login("bob", "bob-pw", ""); st != 403 {
		t.Fatalf("no matching group and no default role: %d", st)
	}
	if st, _ := login("admin", "directory-pw", ""); st != 403 {
		t.Fatalf("local account must not fall through to the directory: %d", st)
	}
	if st, _ := login("x)(uid=alice", "alice-pw", ""); st != 403 {
		t.Fatalf("filter injection: %d", st)
	}
	dir.mu.Lock()
	escaped := false
	for _, f := range dir.filters {
		escaped = escaped || strings.Contains(f, `x\29\28uid=alice`)
	}
	dir.mu.Unlock()
	if !escaped {
		t.Fatalf("username must be escaped in the filter: %v", dir.filters)
	}
	// group change → role follows on next login; default role admits bob
	dir.mu.Lock()
	dir.users["alice"] = fakeEntry{dn: dir.users["alice"].dn, password: "alice-pw", groups: []string{"cn=ops," + groups}}
	dir.mu.Unlock()
	set(map[string]string{"ldap.default_role": "viewer"})
	if _, o := login("alice", "alice-pw", ""); o["user"].(map[string]any)["role"] != "operator" {
		t.Fatalf("role must follow groups: %v", o["user"])
	}
	if _, o := login("bob", "bob-pw", ""); o["user"].(map[string]any)["role"] != "viewer" {
		t.Fatalf("default role: %v", o)
	}
	saved := h.tok
	h.tok = alice
	var me map[string]any
	if h.do("GET", "/v1/auth/me", nil, &me); me["username"] != "alice" {
		t.Fatalf("ldap session: %v", me)
	}
	h.tok = saved
	var us []auth.User
	h.do("GET", "/v1/users", nil, &us)
	n := 0
	for _, u := range us {
		if u.AuthSource == "ldap" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("ldap users provisioned: %d %+v", n, us)
	}

	// 5 wrong directory passwords lock the account like a local one
	for i := 0; i < 5; i++ {
		login("bob", "wrong", "")
	}
	if st, o := login("bob", "bob-pw", ""); st != 403 || !strings.Contains(o["error"].(map[string]any)["message"].(string), "locked") {
		t.Fatalf("lockout: %d %v", st, o)
	}

	// ldaps:// with a private CA: refused with system roots, accepted with ldap.ca_file
	ca, _ := pki.LoadOrCreate(t.TempDir(), "ldap-test")
	tc, _ := ca.ServerTLS([]string{"127.0.0.1"})
	sec := newFakeLDAP(t, tc)
	sec.users = dir.users
	set(map[string]string{"ldap.url": "ldaps://" + sec.ln.Addr().String()})
	if st, _ := login("alice", "alice-pw", ""); st != 409 {
		t.Fatalf("untrusted directory certificate: %d", st)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw}), 0o644)
	set(map[string]string{"ldap.ca_file": caFile})
	if st, o := login("alice", "alice-pw", ""); st != 200 {
		t.Fatalf("ldaps with ldap.ca_file: %d %v", st, o)
	}
}
