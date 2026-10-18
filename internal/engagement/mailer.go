package engagement

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/config"
)

type Message struct {
	To      string
	Subject string
	Text    string
	Headers map[string]string
}

type Mailer interface {
	Enabled() bool
	Send(ctx context.Context, message Message) error
}

type SMTPMailer struct {
	config config.SMTPConfig
	dialer *net.Dialer
}

func NewSMTPMailer(cfg config.SMTPConfig) *SMTPMailer {
	return &SMTPMailer{config: cfg, dialer: &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}}
}

func (m *SMTPMailer) Enabled() bool { return m != nil && m.config.Configured() }

func (m *SMTPMailer) Send(ctx context.Context, message Message) error {
	if !m.Enabled() {
		return fmt.Errorf("%w: SMTP delivery", ErrNotConfigured)
	}
	to, err := NormalizeEmail(message.To)
	if err != nil {
		return err
	}
	from, err := NormalizeEmail(m.config.From)
	if err != nil {
		return fmt.Errorf("%w: SMTP sender", ErrNotConfigured)
	}
	payload, err := m.message(from, to, message)
	if err != nil {
		return err
	}
	host := m.config.Host
	address := net.JoinHostPort(host, strconv.Itoa(m.config.Port))
	dialer := m.dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 15 * time.Second}
	}
	connection, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("SMTP connection failed: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(30 * time.Second)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	if m.config.TLSMode == "tls" {
		tlsConnection := tls.Client(connection, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS negotiation failed")
		}
		connection = tlsConnection
	}
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return errors.New("SMTP initialization failed")
	}
	defer client.Close()
	if m.config.TLSMode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("SMTP server does not support STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return errors.New("SMTP STARTTLS negotiation failed")
		}
	} else if m.config.TLSMode != "none" && m.config.TLSMode != "tls" {
		return fmt.Errorf("%w: unsupported SMTP TLS mode", ErrNotConfigured)
	}
	if m.config.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.config.Username, m.config.Password, host)); err != nil {
			return errors.New("SMTP authentication failed")
		}
	}
	if err := client.Mail(from); err != nil {
		return errors.New("SMTP sender was rejected")
	}
	if err := client.Rcpt(to); err != nil {
		return errors.New("SMTP recipient was rejected")
	}
	writer, err := client.Data()
	if err != nil {
		return errors.New("SMTP message data was rejected")
	}
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return errors.New("SMTP message write failed")
	}
	if err := writer.Close(); err != nil {
		return errors.New("SMTP message was not accepted")
	}
	return client.Quit()
}

func (m *SMTPMailer) message(from, to string, message Message) ([]byte, error) {
	if strings.ContainsAny(message.Subject+m.config.FromName, "\r\n\x00") || len(message.Subject) > 500 {
		return nil, fmt.Errorf("%w: invalid email subject or sender name", ErrInvalidInput)
	}
	if len(message.Text) == 0 || len(message.Text) > 2<<20 {
		return nil, fmt.Errorf("%w: email body is empty or too large", ErrInvalidInput)
	}
	var output bytes.Buffer
	fromAddress := mail.Address{Name: m.config.FromName, Address: from}
	output.WriteString("From: " + fromAddress.String() + "\r\n")
	output.WriteString("To: " + to + "\r\n")
	output.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", message.Subject) + "\r\n")
	output.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	output.WriteString("MIME-Version: 1.0\r\n")
	output.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	output.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
	for name, value := range message.Headers {
		if !validMailHeader(name, value) {
			return nil, fmt.Errorf("%w: invalid email header", ErrInvalidInput)
		}
		if strings.HasPrefix(strings.ToLower(name), "list-unsubscribe") {
			if _, err := url.Parse(strings.Trim(value, "<>")); err != nil {
				return nil, fmt.Errorf("%w: invalid unsubscribe URL", ErrInvalidInput)
			}
		}
		output.WriteString(name + ": " + value + "\r\n")
	}
	output.WriteString("\r\n")
	writer := quotedprintable.NewWriter(&output)
	if _, err := writer.Write([]byte(message.Text)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func validMailHeader(name, value string) bool {
	if name == "" || len(name) > 100 || len(value) > 4096 || strings.ContainsAny(name+value, "\r\n\x00") {
		return false
	}
	switch strings.ToLower(name) {
	case "from", "to", "subject", "date", "mime-version", "content-type", "content-transfer-encoding":
		return false
	}
	for _, r := range name {
		if r <= 32 || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?=", r) {
			return false
		}
	}
	return true
}
