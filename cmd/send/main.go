// send is a parameterized SMTP send CLI built on neomd's smtp package.
// Mirrors cmd/sendtest but accepts subject, body, and recipient(s) on the
// command line so it can be used non-interactively from scripts and other
// tools without hand-editing source.
//
// Usage:
//
//	neomd-send -to <addr> -subject "..." [-account NAME] [-cc ...] [-bcc ...] \
//	          [-attach path -attach path ...] < body.md
//
// Body is read from stdin as Markdown. neomd's smtp.Send renders it to a
// multipart/alternative message (text/plain + goldmark HTML) and routes
// through the configured account.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/smtp"
)

type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var (
		to          string
		cc          string
		bcc         string
		subject     string
		account     string
		attachments stringSlice
		bodyFile    string
	)

	flag.StringVar(&to, "to", "", "recipient address (required)")
	flag.StringVar(&cc, "cc", "", "CC address(es), comma-separated")
	flag.StringVar(&bcc, "bcc", "", "BCC address(es), comma-separated")
	flag.StringVar(&subject, "subject", "", "subject line (required)")
	flag.StringVar(&account, "account", "", "account name from config (default: first)")
	flag.StringVar(&bodyFile, "body-file", "", "read body from file instead of stdin")
	flag.Var(&attachments, "attach", "attachment path (repeatable)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s -to ADDR -subject \"...\" [flags] < body.md\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if to == "" || subject == "" {
		flag.Usage()
		os.Exit(2)
	}

	body, err := readBody(bodyFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "neomd-send: read body: %v\n", err)
		os.Exit(1)
	}
	if strings.TrimSpace(body) == "" {
		fmt.Fprintln(os.Stderr, "neomd-send: body is empty")
		os.Exit(1)
	}

	cfg, err := config.Load("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "neomd-send: load config: %v\n", err)
		os.Exit(1)
	}

	acc, err := pickAccount(cfg, account)
	if err != nil {
		fmt.Fprintf(os.Stderr, "neomd-send: %v\n", err)
		os.Exit(1)
	}

	host, port := splitAddr(acc.SMTP)
	smtpCfg := smtp.Config{
		Host:        host,
		Port:        port,
		User:        acc.User,
		Password:    acc.Password,
		From:        acc.From,
		STARTTLS:    acc.STARTTLS,
		TLSCertFile: acc.TLSCertFile,
	}

	if err := smtp.Send(smtpCfg, to, cc, bcc, subject, body, attachments); err != nil {
		fmt.Fprintf(os.Stderr, "neomd-send: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sent: %q via %s -> %s\n", subject, acc.Name, to)
}

func readBody(path string) (string, error) {
	if path != "" {
		b, err := os.ReadFile(path)
		return string(b), err
	}
	b, err := io.ReadAll(os.Stdin)
	return string(b), err
}

func pickAccount(cfg *config.Config, name string) (config.AccountConfig, error) {
	accounts := cfg.ActiveAccounts()
	if len(accounts) == 0 {
		return config.AccountConfig{}, fmt.Errorf("no accounts configured")
	}
	if name == "" {
		return accounts[0], nil
	}
	for _, acc := range accounts {
		if strings.EqualFold(acc.Name, name) {
			return acc, nil
		}
	}
	available := make([]string, 0, len(accounts))
	for _, acc := range accounts {
		available = append(available, acc.Name)
	}
	return config.AccountConfig{}, fmt.Errorf("account %q not found; available: %s", name, strings.Join(available, ", "))
}

func splitAddr(addr string) (host, port string) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "587"
	}
	return addr[:i], addr[i+1:]
}
