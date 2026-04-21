# Email Standards Compliance

neomd implements modern email standards to tries for maximum deliverability and compatibility across all email clients and spam filters.

## RFC Compliance

### MIME Structure (RFC 2045, 2046)

**multipart/alternative** - All sent emails include both plain text and HTML versions:
- **Plain text first, HTML second** (RFC 2046 requirement for backwards compatibility)
- Plain text uses formatted callouts (emoji + text) for readability
- HTML uses full goldmark rendering with styled callout boxes

**Encoding**: Quoted-printable (RFC 2045) for both text/plain and text/html parts
- Human-readable for ASCII content
- Efficient encoding for international characters
- Spam-filter friendly (modern filters decode before analysis)

**MIME Structure Patterns**:
```
No attachments     → multipart/alternative (text/plain + text/html)
File attachments   → multipart/mixed > multipart/alternative + files
Inline images only → multipart/related > (multipart/alternative + images)
Both               → multipart/mixed > (multipart/related > alt+images) + files
```

### Required Headers (RFC 5322)

**Always Present**:
- `From:` - Sender address from config
- `To:` - Recipient(s)
- `Cc:` - Carbon copy recipients (when applicable)
- `Subject:` - Q-encoded for international characters
- `Date:` - RFC1123Z format (e.g., `Tue, 21 Apr 2026 20:05:09 +0200`)
- `Message-ID:` - **Uses sender's domain** (e.g., `<hex@ssp.sh>`, not `@neomd`)
- `MIME-Version: 1.0` - Required for multipart emails
- `Content-Type:` - Specifies MIME structure and boundaries
- `X-Mailer: neomd` - Identifies the client (minimal spam impact)

**Threading Headers** (when replying/forwarding):
- `In-Reply-To:` - Message-ID of the email being replied to
- `References:` - Full thread chain (preserves conversation context)

**Never Included**:
- `Bcc:` - Intentionally excluded from headers (RFC 5322 privacy requirement)
  - BCC recipients receive the email via SMTP RCPT TO
  - They never appear in message headers (standard BCC behavior)

### Message-ID Best Practice

Message-IDs use the **sender's domain** extracted from the `From:` address:

```
From: Simon Späti <simon@ssp.sh>
Message-ID: <4f6bfc2ad10d7787a822295d@ssp.sh>
              ^^^^^^^^^^^^^^^^^^^^^^^ ^^^^^^^
              random hex              sender's domain
```

**Why this matters:**
- RFC 5322 recommends Message-IDs contain a fully qualified domain name you control
- Some spam filters check for domain consistency
- Improves email threading across clients

**Implementation:** `internal/smtp/sender.go:400,431,463,706-723`

## Authentication Requirements (2026 Standards)

### SPF (Sender Policy Framework)

**What neomd does:** Nothing - SPF is configured in DNS by the domain owner.

**What you must do:**
1. Add SPF TXT record to your domain's DNS
2. Include your SMTP server's IP or domain
3. Example: `v=spf1 include:amazonses.com redirect=spf.mail.hostpoint.ch`

**Verification:** `dig your-domain.com TXT +short | grep spf`

### DKIM (DomainKeys Identified Mail)

**What neomd does:** Nothing - DKIM signing is done by your SMTP server.

**What you must do:**
1. Enable DKIM signing on your SMTP provider (Hostpoint, Gmail, AWS SES, etc.)
2. Add DKIM public key TXT record to DNS
3. Verify signing by checking raw email headers for `DKIM-Signature:`

**Example (Hostpoint signs with 3 algorithms):**
```
DKIM-Signature: v=1; a=rsa-sha256; d=ssp.sh; s=20241021-rsa1024-...
DKIM-Signature: v=1; a=rsa-sha256; d=ssp.sh; s=20241021-rsa2048-...
DKIM-Signature: v=1; a=ed25519-sha256; d=ssp.sh; s=20241021-ed25519-...
```

### DMARC (Domain-based Message Authentication)

**What neomd does:** Nothing - DMARC is a DNS policy record.

**What you must do:**
1. Add `_dmarc` TXT record to DNS
2. Start with `p=quarantine` for monitoring
3. After 2-4 weeks, escalate to `p=reject` for full protection
4. Add `rua=` for daily reports
5. Example: `v=DMARC1; p=quarantine; rua=mailto:dmarc@your-domain.com; pct=100; adkim=s; aspf=s;`

**Verification:** `dig _dmarc.your-domain.com TXT +short`

**DMARC Policy Changes:**
- DMARC policies are set in **DNS by the domain owner**
- **Not controlled by your SMTP provider** (Hostpoint, Gmail, etc.)
- You can change DMARC policy independent of your email provider
- Changes propagate according to DNS TTL (usually within minutes to hours)

## Testing Your Configuration

After setting up DNS records, verify deliverability:

1. **mail-tester.com** - Comprehensive spam score (aim for 10/10)
2. **mxtoolbox.com/deliverability** - Check SPF, DKIM, DMARC
3. **Google Admin Toolbox** - Test Gmail delivery
4. **Send test emails** - Check raw headers for authentication results

**Looking for in raw email:**
```
Authentication-Results: receiving-server.com;
    dkim=pass header.d=your-domain.com
    spf=pass smtp.mailfrom=you@your-domain.com
    dmarc=pass (policy=quarantine) header.from=your-domain.com
```

All three must show `pass` for optimal deliverability.

## Architecture Notes

**Separation of Concerns:**
- **neomd (this client):** Generates RFC-compliant MIME messages
- **Your SMTP server:** Signs with DKIM, enforces SPF/DMARC
- **Your DNS:** Publishes SPF, DKIM keys, DMARC policy

**Why the split?**
- Email clients should not manage cryptographic keys
- DKIM signing requires server-side infrastructure
- DNS configuration is provider-independent

**Result:** neomd focuses on correctness of MIME generation; your SMTP provider handles authentication.

## Further Reading

- [RFC 5322](https://www.rfc-editor.org/rfc/rfc5322) - Internet Message Format
- [RFC 2045-2049](https://www.rfc-editor.org/rfc/rfc2045) - MIME specification
- [RFC 6376](https://www.rfc-editor.org/rfc/rfc6376) - DKIM Signatures
- [RFC 7208](https://www.rfc-editor.org/rfc/rfc7208) - SPF
- [RFC 7489](https://www.rfc-editor.org/rfc/rfc7489) - DMARC
- [Google/Yahoo 2026 Requirements](https://dmarcly.com/blog/how-to-implement-dmarc-dkim-spf-to-stop-email-spoofing-phishing-the-definitive-guide) - Current best practices
