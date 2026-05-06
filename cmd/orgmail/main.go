// orgmail is a small CLI for inspecting and reorganizing a mailbox using
// neomd's IMAP plumbing. Reads creds from ~/.config/neomd/config.toml.
//
// Usage:
//
//	orgmail [-account NAME] <subcommand> [args...]
//
// Subcommands:
//
//	folders                            List every mailbox with total/unseen counts.
//	list <folder> [-n N] [-from STR] [-since DATE] [-unread]
//	                                   Show recent message headers in a folder.
//	show <folder> <uid>                Print one message body (text/plain).
//	search <query> [-folder NAME]      Free-text search; by default scans all folders.
//	move <src> <uids> <dst> [-confirm] Move messages (uids = comma-list, e.g. 12,17,20).
//	mark-read <folder> <uids> [-confirm]
//	delete <folder> <uids> [-confirm]
//	delete-folder <folder> [-confirm]  Remove an empty mailbox on the server.
//	senders <folder> [-before DATE] [-top N]
//	                                   Group messages by sender (recon for cleanup).
//	bulk-move <src> <dst> [-from-domain D] [-before DATE] [-confirm]
//	                                   Move every message in src whose From or
//	                                   internal date matches the criteria to dst.
//
// Without -confirm, destructive verbs print what they would do and exit.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
)

var account string

func main() {
	flag.StringVar(&account, "account", "", "account name from neomd config (default: first)")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: orgmail [-account NAME] <subcommand> [args...]")
		fmt.Fprintln(os.Stderr, "subcommands: folders, list, show, search, move, mark-read, delete")
	}
	flag.Parse()

	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	sub := flag.Arg(0)
	args := flag.Args()[1:]

	client, accLabel, err := connect()
	if err != nil {
		die("connect: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	switch sub {
	case "folders":
		runFolders(ctx, client, accLabel)
	case "list":
		runList(ctx, client, args)
	case "show":
		runShow(ctx, client, args)
	case "search":
		runSearch(ctx, client, args)
	case "move":
		runMove(ctx, client, args)
	case "mark-read":
		runMarkRead(ctx, client, args)
	case "delete":
		runDelete(ctx, client, args)
	case "delete-folder":
		runDeleteFolder(ctx, client, args)
	case "senders":
		runSenders(ctx, client, args)
	case "bulk-move":
		runBulkMove(ctx, client, args)
	case "archive-old":
		runArchiveOld(ctx, client, args)
	default:
		die("unknown subcommand: %s", sub)
	}
}

func connect() (*imap.Client, string, error) {
	cfg, err := config.Load("")
	if err != nil {
		return nil, "", fmt.Errorf("load config: %w", err)
	}
	accs := cfg.ActiveAccounts()
	if len(accs) == 0 {
		return nil, "", fmt.Errorf("no accounts configured in ~/.config/neomd/config.toml")
	}
	var acc *config.AccountConfig
	for i := range accs {
		if account == "" || strings.EqualFold(accs[i].Name, account) {
			acc = &accs[i]
			break
		}
	}
	if acc == nil {
		names := make([]string, len(accs))
		for i, a := range accs {
			names[i] = a.Name
		}
		return nil, "", fmt.Errorf("account %q not found; available: %s", account, strings.Join(names, ", "))
	}

	host, port := splitAddr(acc.IMAP)
	imapCfg := imap.Config{
		Host:     host,
		Port:     port,
		User:     acc.User,
		Password: acc.Password,
		TLS:      port == "993",
	}
	c := imap.New(imapCfg)
	if err := c.Ping(context.Background()); err != nil {
		return nil, "", fmt.Errorf("ping: %w", err)
	}
	return c, acc.Name, nil
}

func runFolders(ctx context.Context, c *imap.Client, accLabel string) {
	mbs, err := c.ListMailboxes(ctx)
	if err != nil {
		die("list mailboxes: %v", err)
	}
	fmt.Printf("# %s — %d mailboxes\n", accLabel, len(mbs))
	fmt.Printf("%-50s  %8s  %8s\n", "MAILBOX", "TOTAL", "UNSEEN")
	for _, m := range mbs {
		name := m.Name
		if !m.Selectable {
			name += " (no-select)"
		}
		fmt.Printf("%-50s  %8d  %8d\n", truncate(name, 50), m.Total, m.Unseen)
	}
}

func runList(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	n := fs.Int("n", 50, "max messages")
	fromFilter := fs.String("from", "", "filter by sender substring (case-insensitive)")
	since := fs.String("since", "", "filter to messages on/after YYYY-MM-DD")
	unreadOnly := fs.Bool("unread", false, "only unread messages")
	fs.Parse(args)
	if fs.NArg() < 1 {
		die("usage: orgmail list <folder> [-n N] [-from STR] [-since DATE] [-unread]")
	}
	folder := fs.Arg(0)

	emails, err := c.FetchLatest(ctx, folder, *n)
	if err != nil {
		die("fetch: %v", err)
	}

	var sinceT time.Time
	if *since != "" {
		t, err := time.Parse("2006-01-02", *since)
		if err != nil {
			die("invalid -since date: %v", err)
		}
		sinceT = t
	}

	count := 0
	fmt.Printf("%-6s  %1s  %-30s  %-50s  %s\n", "UID", "R", "FROM", "SUBJECT", "DATE")
	for _, e := range emails {
		if *unreadOnly && e.Seen {
			continue
		}
		if *fromFilter != "" && !strings.Contains(strings.ToLower(e.From), strings.ToLower(*fromFilter)) {
			continue
		}
		if !sinceT.IsZero() && e.Date.Before(sinceT) {
			continue
		}
		flag := " "
		if !e.Seen {
			flag = "*"
		}
		fmt.Printf("%-6d  %s  %-30s  %-50s  %s\n",
			e.UID, flag,
			truncate(e.From, 30),
			truncate(e.Subject, 50),
			e.Date.Format("2006-01-02 15:04"))
		count++
	}
	fmt.Printf("\n# %d shown (of %d fetched)\n", count, len(emails))
}

func runShow(ctx context.Context, c *imap.Client, args []string) {
	if len(args) < 2 {
		die("usage: orgmail show <folder> <uid>")
	}
	folder := args[0]
	uid64, err := strconv.ParseUint(args[1], 10, 32)
	if err != nil {
		die("invalid uid: %v", err)
	}
	uid := uint32(uid64)

	plain, html, _, _, _, _, err := c.FetchBody(ctx, folder, uid)
	if err != nil {
		die("fetch body: %v", err)
	}
	if plain != "" {
		fmt.Println(plain)
		return
	}
	if html != "" {
		fmt.Println("(no text/plain — falling back to HTML source)")
		fmt.Println(html)
		return
	}
	fmt.Println("(empty body)")
}

func runSearch(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	folderOnly := fs.String("folder", "", "limit to one folder; default: all selectable folders")
	fs.Parse(args)
	if fs.NArg() < 1 {
		die("usage: orgmail search <query> [-folder NAME]")
	}
	query := strings.Join(fs.Args(), " ")

	var emails []imap.Email
	if *folderOnly != "" {
		out, err := c.SearchMessages(ctx, *folderOnly, query)
		if err != nil {
			die("search: %v", err)
		}
		emails = out
	} else {
		mbs, err := c.ListMailboxes(ctx)
		if err != nil {
			die("list mailboxes: %v", err)
		}
		var folders []string
		for _, m := range mbs {
			if m.Selectable {
				folders = append(folders, m.Name)
			}
		}
		out, err := c.SearchAllFolders(ctx, folders, query)
		if err != nil {
			die("search: %v", err)
		}
		emails = out
	}

	fmt.Printf("%-6s  %-20s  %-30s  %-50s  %s\n", "UID", "FOLDER", "FROM", "SUBJECT", "DATE")
	for _, e := range emails {
		fmt.Printf("%-6d  %-20s  %-30s  %-50s  %s\n",
			e.UID,
			truncate(e.Folder, 20),
			truncate(e.From, 30),
			truncate(e.Subject, 50),
			e.Date.Format("2006-01-02 15:04"))
	}
	fmt.Printf("\n# %d hits\n", len(emails))
}

func runMove(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("move", flag.ExitOnError)
	confirm := fs.Bool("confirm", false, "actually move (otherwise dry-run)")
	fs.Parse(args)
	if fs.NArg() < 3 {
		die("usage: orgmail move <src> <uids> <dst> [-confirm]")
	}
	src, uidsRaw, dst := fs.Arg(0), fs.Arg(1), fs.Arg(2)
	uids, err := parseUIDs(uidsRaw)
	if err != nil {
		die("uids: %v", err)
	}
	if !*confirm {
		fmt.Printf("[dry-run] would move %d msg(s) from %q to %q (uids: %v)\n", len(uids), src, dst, uids)
		return
	}
	for _, u := range uids {
		newUID, err := c.MoveMessage(ctx, src, u, dst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "move uid=%d: %v\n", u, err)
			continue
		}
		fmt.Printf("moved uid=%d → %s (new uid=%d)\n", u, dst, newUID)
	}
}

func runMarkRead(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("mark-read", flag.ExitOnError)
	confirm := fs.Bool("confirm", false, "actually mark (otherwise dry-run)")
	fs.Parse(args)
	if fs.NArg() < 2 {
		die("usage: orgmail mark-read <folder> <uids> [-confirm]")
	}
	folder := fs.Arg(0)
	uids, err := parseUIDs(fs.Arg(1))
	if err != nil {
		die("uids: %v", err)
	}
	if !*confirm {
		fmt.Printf("[dry-run] would mark %d msg(s) read in %q (uids: %v)\n", len(uids), folder, uids)
		return
	}
	for _, u := range uids {
		if err := c.MarkSeen(ctx, folder, u); err != nil {
			fmt.Fprintf(os.Stderr, "mark uid=%d: %v\n", u, err)
			continue
		}
		fmt.Printf("marked seen uid=%d in %s\n", u, folder)
	}
}

func runDelete(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	confirm := fs.Bool("confirm", false, "actually delete (otherwise dry-run)")
	fs.Parse(args)
	if fs.NArg() < 2 {
		die("usage: orgmail delete <folder> <uids> [-confirm]")
	}
	folder := fs.Arg(0)
	uids, err := parseUIDs(fs.Arg(1))
	if err != nil {
		die("uids: %v", err)
	}
	if !*confirm {
		fmt.Printf("[dry-run] would delete %d msg(s) from %q (uids: %v)\n", len(uids), folder, uids)
		return
	}
	if err := c.ExpungeAll(ctx, folder, uids); err != nil {
		die("delete: %v", err)
	}
	fmt.Printf("deleted %d msg(s) from %s\n", len(uids), folder)
}

func runSenders(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("senders", flag.ExitOnError)
	before := fs.String("before", "", "only count messages with Date header before YYYY-MM-DD (client-side filter)")
	top := fs.Int("top", 50, "show top N senders")
	fs.Parse(args)
	if fs.NArg() < 1 {
		die("usage: orgmail senders <folder> [-before YYYY-MM-DD] [-top N]")
	}
	folder := fs.Arg(0)

	var cutoff time.Time
	if *before != "" {
		t, err := time.Parse("2006-01-02", *before)
		if err != nil {
			die("invalid -before: %v", err)
		}
		cutoff = t
	}

	uids, err := c.SearchUIDs(ctx, folder)
	if err != nil {
		die("search: %v", err)
	}
	fmt.Printf("# %d total messages in %s\n", len(uids), folder)
	if len(uids) == 0 {
		return
	}

	type stat struct {
		count    int
		sample   string
		latest   time.Time
		earliest time.Time
	}
	bySender := map[string]*stat{}
	matched := 0

	const batch = 1000
	for i := 0; i < len(uids); i += batch {
		end := i + batch
		if end > len(uids) {
			end = len(uids)
		}
		emails, err := c.FetchHeadersByUID(ctx, folder, uids[i:end])
		if err != nil {
			die("fetch headers (batch %d): %v", i/batch, err)
		}
		for _, e := range emails {
			if !cutoff.IsZero() && !e.Date.Before(cutoff) {
				continue
			}
			matched++
			key := normalizeSender(e.From)
			s, ok := bySender[key]
			if !ok {
				s = &stat{sample: e.Subject, latest: e.Date, earliest: e.Date}
				bySender[key] = s
			}
			s.count++
			if e.Date.After(s.latest) {
				s.latest = e.Date
			}
			if e.Date.Before(s.earliest) {
				s.earliest = e.Date
			}
		}
		fmt.Fprintf(os.Stderr, "  fetched %d/%d\r", end, len(uids))
	}
	fmt.Fprintln(os.Stderr)
	if !cutoff.IsZero() {
		fmt.Printf("# %d messages with Date < %s\n", matched, cutoff.Format("2006-01-02"))
	}

	type row struct {
		sender string
		s      *stat
	}
	rows := make([]row, 0, len(bySender))
	for k, v := range bySender {
		rows = append(rows, row{k, v})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].s.count > rows[j].s.count })

	fmt.Printf("# %d unique senders. Top %d:\n", len(rows), *top)
	fmt.Printf("%6s  %-40s  %-10s → %-10s  %s\n", "COUNT", "SENDER", "EARLIEST", "LATEST", "SAMPLE SUBJECT")
	limit := *top
	if limit > len(rows) {
		limit = len(rows)
	}
	for _, r := range rows[:limit] {
		fmt.Printf("%6d  %-40s  %-10s → %-10s  %s\n",
			r.s.count,
			truncate(r.sender, 40),
			r.s.earliest.Format("2006-01-02"),
			r.s.latest.Format("2006-01-02"),
			truncate(r.s.sample, 60))
	}
}

func runBulkMove(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("bulk-move", flag.ExitOnError)
	fromDomain := fs.String("from-domain", "", "comma-separated sender substrings; OR-matched, case-insensitive")
	before := fs.String("before", "", "only messages with Date header before YYYY-MM-DD (client-side)")
	confirm := fs.Bool("confirm", false, "actually move (otherwise dry-run)")
	fs.Parse(args)
	if fs.NArg() < 2 {
		die("usage: orgmail bulk-move <src> <dst> [-from-domain s1,s2,...] [-before DATE] [-confirm]")
	}
	src, dst := fs.Arg(0), fs.Arg(1)
	var needles []string
	for _, s := range strings.Split(*fromDomain, ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			needles = append(needles, s)
		}
	}

	var cutoff time.Time
	if *before != "" {
		t, err := time.Parse("2006-01-02", *before)
		if err != nil {
			die("invalid -before: %v", err)
		}
		cutoff = t
	}

	uids, err := c.SearchUIDs(ctx, src)
	if err != nil {
		die("search: %v", err)
	}

	// Always fetch headers when we have any client-side filter
	if len(needles) > 0 || !cutoff.IsZero() {
		var keep []uint32
		const batch = 1000
		for i := 0; i < len(uids); i += batch {
			end := i + batch
			if end > len(uids) {
				end = len(uids)
			}
			emails, err := c.FetchHeadersByUID(ctx, src, uids[i:end])
			if err != nil {
				die("fetch headers: %v", err)
			}
			for _, e := range emails {
				if !cutoff.IsZero() && !e.Date.Before(cutoff) {
					continue
				}
				if len(needles) > 0 {
					from := strings.ToLower(e.From)
					matched := false
					for _, n := range needles {
						if strings.Contains(from, n) {
							matched = true
							break
						}
					}
					if !matched {
						continue
					}
				}
				keep = append(keep, e.UID)
			}
			fmt.Fprintf(os.Stderr, "  scanned %d/%d (matched %d)\r", end, len(uids), len(keep))
		}
		fmt.Fprintln(os.Stderr)
		uids = keep
	}

	if len(uids) == 0 {
		fmt.Println("no matches")
		return
	}
	if !*confirm {
		fmt.Printf("[dry-run] would move %d msg(s) from %q to %q\n", len(uids), src, dst)
		return
	}
	const moveBatch = 1000
	total := 0
	for i := 0; i < len(uids); i += moveBatch {
		end := i + moveBatch
		if end > len(uids) {
			end = len(uids)
		}
		n, err := c.BulkMove(ctx, src, dst, uids[i:end])
		if err != nil {
			fmt.Fprintf(os.Stderr, "batch %d: %v\n", i/moveBatch, err)
			continue
		}
		total += n
		fmt.Fprintf(os.Stderr, "  moved %d/%d\r", total, len(uids))
	}
	fmt.Fprintln(os.Stderr)
	fmt.Printf("moved %d msg(s) from %s → %s\n", total, src, dst)
}

// runArchiveOld is a single-pass cleanup: messages older than -before are
// either deleted (if their From matches one of -delete-senders) or moved to
// -archive. One header fetch, two bulk ops at the end.
func runArchiveOld(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("archive-old", flag.ExitOnError)
	src := fs.String("src", "", "source folder (required)")
	archive := fs.String("archive", "Archive", "archive destination folder")
	before := fs.String("before", "", "messages with Date header before YYYY-MM-DD (required)")
	deleteList := fs.String("delete-senders", "", "comma-separated From substrings; matches go to delete instead of archive")
	confirm := fs.Bool("confirm", false, "actually run the bulk ops (otherwise dry-run)")
	fs.Parse(args)

	if *src == "" || *before == "" {
		die("usage: orgmail archive-old -src FOLDER -before YYYY-MM-DD [-archive FOLDER] [-delete-senders s1,s2,...] [-confirm]")
	}
	cutoff, err := time.Parse("2006-01-02", *before)
	if err != nil {
		die("invalid -before: %v", err)
	}
	var deleteNeedles []string
	for _, s := range strings.Split(*deleteList, ",") {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" {
			deleteNeedles = append(deleteNeedles, s)
		}
	}

	uids, err := c.SearchUIDs(ctx, *src)
	if err != nil {
		die("search: %v", err)
	}
	fmt.Printf("scanning %d total messages in %s...\n", len(uids), *src)

	var toDelete, toArchive []uint32
	const batch = 1000
	for i := 0; i < len(uids); i += batch {
		end := i + batch
		if end > len(uids) {
			end = len(uids)
		}
		emails, err := c.FetchHeadersByUID(ctx, *src, uids[i:end])
		if err != nil {
			die("fetch headers: %v", err)
		}
		for _, e := range emails {
			if !e.Date.Before(cutoff) {
				continue
			}
			from := strings.ToLower(e.From)
			matched := false
			for _, n := range deleteNeedles {
				if strings.Contains(from, n) {
					matched = true
					break
				}
			}
			if matched {
				toDelete = append(toDelete, e.UID)
			} else {
				toArchive = append(toArchive, e.UID)
			}
		}
		fmt.Fprintf(os.Stderr, "  scanned %d/%d (delete=%d archive=%d)\r",
			end, len(uids), len(toDelete), len(toArchive))
	}
	fmt.Fprintln(os.Stderr)

	fmt.Printf("\nResult:\n  DELETE  %d msg(s) matching %d sender pattern(s)\n  ARCHIVE %d msg(s) to %q\n",
		len(toDelete), len(deleteNeedles), len(toArchive), *archive)

	if !*confirm {
		fmt.Println("\n[dry-run — re-run with -confirm to execute]")
		return
	}

	if len(toDelete) > 0 {
		fmt.Printf("\nDeleting %d msg(s)...\n", len(toDelete))
		// ExpungeAll handles bulk via UIDSetNum internally
		if err := c.ExpungeAll(ctx, *src, toDelete); err != nil {
			fmt.Fprintf(os.Stderr, "delete: %v\n", err)
		} else {
			fmt.Printf("  deleted %d\n", len(toDelete))
		}
	}
	if len(toArchive) > 0 {
		fmt.Printf("Archiving %d msg(s) to %s...\n", len(toArchive), *archive)
		const moveBatch = 1000
		moved := 0
		for i := 0; i < len(toArchive); i += moveBatch {
			end := i + moveBatch
			if end > len(toArchive) {
				end = len(toArchive)
			}
			n, err := c.BulkMove(ctx, *src, *archive, toArchive[i:end])
			if err != nil {
				fmt.Fprintf(os.Stderr, "  batch %d: %v\n", i/moveBatch, err)
				continue
			}
			moved += n
			fmt.Fprintf(os.Stderr, "  moved %d/%d\r", moved, len(toArchive))
		}
		fmt.Fprintln(os.Stderr)
		fmt.Printf("  archived %d\n", moved)
	}
}

func normalizeSender(from string) string {
	// Pull email address out of "Name <addr>" if present, else use whole string.
	s := strings.TrimSpace(from)
	if i := strings.LastIndex(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j >= 0 {
			s = s[i+1 : i+j]
		}
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func runDeleteFolder(ctx context.Context, c *imap.Client, args []string) {
	fs := flag.NewFlagSet("delete-folder", flag.ExitOnError)
	confirm := fs.Bool("confirm", false, "actually delete the mailbox (otherwise dry-run)")
	fs.Parse(args)
	if fs.NArg() < 1 {
		die("usage: orgmail delete-folder <folder> [-confirm]")
	}
	folder := fs.Arg(0)
	if !*confirm {
		fmt.Printf("[dry-run] would DELETE mailbox %q\n", folder)
		return
	}
	if err := c.DeleteMailbox(ctx, folder); err != nil {
		die("delete-folder: %v", err)
	}
	fmt.Printf("deleted mailbox %s\n", folder)
}

func parseUIDs(s string) ([]uint32, error) {
	parts := strings.Split(s, ",")
	out := make([]uint32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("uid %q: %w", p, err)
		}
		out = append(out, uint32(v))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no uids parsed")
	}
	return out, nil
}

func splitAddr(s string) (string, string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:]
		}
	}
	return s, ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "orgmail: "+format+"\n", args...)
	os.Exit(1)
}
