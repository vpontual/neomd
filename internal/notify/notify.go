// Package notify fires desktop notifications (via notify-send or compatible
// CLI) for newly arrived emails whose sender is on the screener notify list.
//
// TUI-only: the headless daemon does not invoke this package, so notifications
// never fire on a server / NAS where no one would see them.
package notify

import (
	"os/exec"
	"strconv"
	"strings"

	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/screener"
)

// Notifier wraps a notification command (notify-send by default) and a
// resolved-defaults config snapshot.
type Notifier struct {
	cfg config.NotificationsConfig
}

// New returns a Notifier. Send is a no-op when cfg.Enabled is false.
func New(cfg config.NotificationsConfig) *Notifier {
	return &Notifier{cfg: cfg.Resolved()}
}

// Enabled reports whether notifications would be sent. Useful so callers can
// skip building dstByUID maps when the feature is off.
func (n *Notifier) Enabled() bool {
	return n != nil && n.cfg.Enabled
}

// Send fires a single notification synchronously. Safe to call when disabled
// (returns nil). Errors from the underlying command are swallowed by callers
// — a failed notification should never break the email flow.
func (n *Notifier) Send(title, body string) error {
	if !n.cfg.Enabled {
		return nil
	}
	args := []string{
		"-i", n.cfg.Icon,
		"-t", strconv.Itoa(n.cfg.ExpireMs),
		"-a", "neomd",
		title,
		truncate(body, 200),
	}
	return exec.Command(n.cfg.Command, args...).Run()
}

// MaybeNotify processes a freshly fetched batch of emails from sourceFolder.
// For each email with UID > the per-(account, folder) baseline whose sender is
// in the screener notify list and whose post-screening destination is in the
// configured Folders allowlist, a notification fires.
//
// First-run behaviour: when no baseline exists yet, MaybeNotify silently
// records the highest UID it saw and fires *no* notifications — this prevents
// the entire current Inbox from notifying the first time the feature is
// enabled.
//
// dstByUID maps a UID to the folder label where the email is *about to* live
// after auto-screening (caller computes this from screener.ClassifyForScreen).
// UIDs missing from dstByUID are assumed to stay in sourceFolder.
//
// Returns the number of notifications dispatched.
func (n *Notifier) MaybeNotify(account, sourceFolder string, emails []imap.Email, dstByUID map[uint32]string, sc *screener.Screener, state *State) int {
	if !n.Enabled() || sc == nil || state == nil || len(emails) == 0 {
		return 0
	}
	key := stateKey(account, sourceFolder)
	baseline, hadBaseline := state.Get(key)

	var maxUID uint32
	sent := 0
	for i := range emails {
		e := &emails[i]
		if e.UID > maxUID {
			maxUID = e.UID
		}
		if !hadBaseline || e.UID <= baseline {
			continue
		}
		if !sc.ShouldNotify(e.From) {
			continue
		}
		dst, ok := dstByUID[e.UID]
		if !ok {
			dst = sourceFolder
		}
		if !n.cfg.FolderAllowed(dst) {
			continue
		}
		title := "neomd: " + truncate(e.From, 80)
		body := e.Subject
		if body == "" {
			body = "(no subject)"
		}
		if err := n.Send(title, body); err == nil {
			sent++
		}
	}

	if maxUID > baseline {
		state.Set(key, maxUID)
		_ = state.Save()
	}
	return sent
}

func stateKey(account, folder string) string {
	return account + "|" + folder
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
