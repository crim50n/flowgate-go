package main

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func smartDNSTestDomain() string {
	if domain := os.Getenv("FLOWGATE_SMARTDNS_DOMAIN"); domain != "" {
		return domain
	}
	return "example.com"
}

func encodeDNSName(name string) []byte {
	var out []byte
	for _, label := range splitDNSLabels(name) {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func splitDNSLabels(name string) []string {
	var labels []string
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				labels = append(labels, name[start:i])
			}
			start = i + 1
		}
	}
	return labels
}
func skipDNSName(msg []byte, pos int) (int, error) {
	for {
		if pos >= len(msg) {
			return 0, fmt.Errorf("truncated DNS name")
		}
		length := int(msg[pos])
		if length&0xc0 == 0xc0 {
			if pos+1 >= len(msg) {
				return 0, fmt.Errorf("truncated DNS compression pointer")
			}
			return pos + 2, nil
		}
		pos++
		if length == 0 {
			return pos, nil
		}
		if pos+length > len(msg) {
			return 0, fmt.Errorf("truncated DNS label")
		}
		pos += length
	}
}

type dnsTestResponse struct {
	rcode   int
	answers int
	ipv4    []net.IP
}

func buildDNSQuery(name string, qtype uint16) ([]byte, uint16) {
	id := uint16(0x4242)
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:2], id)
	binary.BigEndian.PutUint16(msg[2:4], 0x0100)
	binary.BigEndian.PutUint16(msg[4:6], 1)
	msg = append(msg, encodeDNSName(name)...)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	msg = binary.BigEndian.AppendUint16(msg, 1)
	return msg, id
}

func rawDNSQuery(name string, qtype uint16) (*dnsTestResponse, error) {
	msg, id := buildDNSQuery(name, qtype)
	conn, err := net.DialTimeout("udp", "127.0.0.1:53", 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(msg); err != nil {
		return nil, err
	}
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return parseDNSResponse(buf[:n], id)
}
func parseDNSResponse(msg []byte, wantID uint16) (*dnsTestResponse, error) {
	if len(msg) < 12 || binary.BigEndian.Uint16(msg[:2]) != wantID {
		return nil, fmt.Errorf("invalid DNS response")
	}
	flags := binary.BigEndian.Uint16(msg[2:4])
	qd := int(binary.BigEndian.Uint16(msg[4:6]))
	an := int(binary.BigEndian.Uint16(msg[6:8]))
	pos := 12
	var err error
	for i := 0; i < qd; i++ {
		pos, err = skipDNSName(msg, pos)
		if err != nil || pos+4 > len(msg) {
			return nil, fmt.Errorf("invalid DNS question")
		}
		pos += 4
	}
	resp := &dnsTestResponse{rcode: int(flags & 0x000f), answers: an}
	for i := 0; i < an; i++ {
		pos, err = skipDNSName(msg, pos)
		if err != nil || pos+10 > len(msg) {
			return nil, fmt.Errorf("invalid DNS answer")
		}
		rtype := binary.BigEndian.Uint16(msg[pos : pos+2])
		rdlen := int(binary.BigEndian.Uint16(msg[pos+8 : pos+10]))
		pos += 10
		if pos+rdlen > len(msg) {
			return nil, fmt.Errorf("truncated DNS rdata")
		}
		if rtype == 1 && rdlen == 4 {
			resp.ipv4 = append(resp.ipv4, net.IPv4(msg[pos], msg[pos+1], msg[pos+2], msg[pos+3]))
		}
		pos += rdlen
	}
	return resp, nil
}
func TestSmartDNSResponseSemantics(t *testing.T) {
	if os.Getenv("FLOWGATE_INTEGRATION") != "1" {
		t.Skip("requires Flowgate runtime integration container")
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	wantIP := net.ParseIP(cfg.Settings.ProxyIP).To4()
	if wantIP == nil {
		t.Fatalf("invalid proxy IP %q", cfg.Settings.ProxyIP)
	}
	for _, tc := range []struct {
		qtype   uint16
		answers int
	}{
		{1, 1},  // A
		{28, 0}, // AAAA
		{64, 0}, // SVCB
		{65, 0}, // HTTPS
	} {
		resp, err := rawDNSQuery(smartDNSTestDomain(), tc.qtype)
		if err != nil {
			t.Fatal(err)
		}
		if resp.rcode != 0 || resp.answers != tc.answers {
			t.Fatalf("qtype %d: rcode=%d answers=%d", tc.qtype, resp.rcode, resp.answers)
		}
		if tc.qtype == 1 && (len(resp.ipv4) != 1 || !resp.ipv4[0].Equal(wantIP)) {
			t.Fatalf("A answer = %v, want %s", resp.ipv4, wantIP)
		}
	}
}
func TestSmartDNSTLSPassthrough(t *testing.T) {
	if os.Getenv("FLOWGATE_INTEGRATION") != "1" {
		t.Skip("requires Flowgate runtime integration container")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", "127.0.0.1:443", &tls.Config{
		ServerName: smartDNSTestDomain(),
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	state := conn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		t.Fatal("upstream TLS certificate missing")
	}
	if err := state.PeerCertificates[0].VerifyHostname(smartDNSTestDomain()); err != nil {
		t.Fatalf("passthrough returned wrong certificate: %v", err)
	}
}
func rawDoTDNSQuery(name string, qtype uint16) (*dnsTestResponse, error) {
	msg, id := buildDNSQuery(name, qtype)
	dialer := &net.Dialer{Timeout: time.Second}
	var conn *tls.Conn
	var err error
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err = tls.DialWithDialer(dialer, "tcp", "127.0.0.1:853", &tls.Config{
			InsecureSkipVerify: true, // integration fixture uses a local snakeoil certificate
			MinVersion:         tls.VersionTLS12,
		})
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	frame := binary.BigEndian.AppendUint16(nil, uint16(len(msg)))
	frame = append(frame, msg...)
	if _, err := conn.Write(frame); err != nil {
		return nil, err
	}
	var size [2]byte
	if _, err := io.ReadFull(conn, size[:]); err != nil {
		return nil, err
	}
	resp := make([]byte, int(binary.BigEndian.Uint16(size[:])))
	if _, err := io.ReadFull(conn, resp); err != nil {
		return nil, err
	}
	return parseDNSResponse(resp, id)
}
func TestSmartDNSDoTResponseSemantics(t *testing.T) {
	if os.Getenv("FLOWGATE_INTEGRATION") != "1" {
		t.Skip("requires Flowgate runtime integration container")
	}
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	wantIP := net.ParseIP(cfg.Settings.ProxyIP).To4()
	for _, tc := range []struct {
		qtype   uint16
		answers int
	}{
		{1, 1},
		{28, 0},
		{64, 0},
		{65, 0},
	} {
		resp, err := rawDoTDNSQuery(smartDNSTestDomain(), tc.qtype)
		if err != nil {
			t.Fatal(err)
		}
		if resp.rcode != 0 || resp.answers != tc.answers {
			t.Fatalf("DoT qtype %d: rcode=%d answers=%d", tc.qtype, resp.rcode, resp.answers)
		}
		if tc.qtype == 1 && (len(resp.ipv4) != 1 || !resp.ipv4[0].Equal(wantIP)) {
			t.Fatalf("DoT A answer = %v, want %s", resp.ipv4, wantIP)
		}
	}
}
