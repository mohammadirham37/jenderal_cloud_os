// Package mail sends transactional email via SMTP when configured.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// ErrDisabled is returned when SMTP is not configured.
var ErrDisabled = errors.New("email delivery is not configured")

// Sender sends plain-text email.
type Sender interface {
	Send(ctx context.Context, to, subject, body string) error
	Enabled() bool
}

// SMTP is an SMTP sender using STARTTLS when offered.
type SMTP struct {
	Host, Username, Password, From string
	Port                           int
}

// Enabled reports whether the sender is configured.
func (s *SMTP) Enabled() bool { return s != nil && s.Host != "" && s.From != "" }

// Send delivers a message.
func (s *SMTP) Send(ctx context.Context, to, subject, body string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("invalid header value")
	}
	addr := net.JoinHostPort(s.Host, fmt.Sprint(s.Port))
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: " + s.From + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n\r\n" + body
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}
