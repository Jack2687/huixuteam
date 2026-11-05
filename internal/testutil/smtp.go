package testutil

import (
	"encoding/base64"
	"io"
	"net"
	"net/mail"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Loopback-only SMTP sink. Never forwards or contacts any real mailbox.
type MailSink struct {
	Host     string
	Port     int
	Messages chan string
	listener net.Listener
	wg       sync.WaitGroup
}

func SMTP(t *testing.T) *MailSink {
	t.Helper()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	sink := &MailSink{Host: host, Port: n, Messages: make(chan string, 50), listener: ln}
	sink.wg.Add(1)
	go func() {
		defer sink.wg.Done()
		for {
			conn, e := ln.Accept()
			if e != nil {
				return
			}
			sink.wg.Add(1)
			go func() {
				defer sink.wg.Done()
				defer conn.Close()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				c := textproto.NewConn(conn)
				c.PrintfLine("220 local.test ESMTP")
				for {
					line, e := c.ReadLine()
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						c.PrintfLine("250 local.test")
					case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"), line == "RSET":
						c.PrintfLine("250 OK")
					case line == "DATA":
						c.PrintfLine("354 End with dot")
						raw, e := c.ReadDotBytes()
						if e != nil {
							return
						}
						sink.Messages <- string(raw)
						c.PrintfLine("250 queued")
					case line == "QUIT":
						c.PrintfLine("221 bye")
						return
					default:
						c.PrintfLine("500 Unsupported")
					}
				}
			}()
		}
	}()
	t.Cleanup(func() { ln.Close(); sink.wg.Wait() })
	return sink
}
func (s *MailSink) Code(t *testing.T) string {
	t.Helper()
	select {
	case raw := <-s.Messages:
		message, e := mail.ReadMessage(strings.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		body, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, message.Body))
		if e != nil {
			t.Fatal(e)
		}
		code := regexp.MustCompile(`[0-9]{6}`).FindString(string(body))
		if code == "" {
			t.Fatal("mail contains no code")
		}
		return code
	case <-time.After(3 * time.Second):
		t.Fatal("email not delivered")
		return ""
	}
}
