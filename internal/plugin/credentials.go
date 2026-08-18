package plugin

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"time"
)

func (p *Plugin) commandStage(args []string, stdin io.Reader) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vault-agent:stage APP --revision FULL_SHA --ttl-seconds N")
	}
	app := args[0]
	if err := validateAppName(app); err != nil {
		return err
	}
	flags, booleans, err := splitFlags(args[1:])
	if err != nil {
		return err
	}
	if len(booleans) != 0 {
		return fmt.Errorf("--replace is not valid for stage")
	}
	if err := rejectUnknownFlags(flags, "revision", "ttl-seconds"); err != nil {
		return err
	}
	revision, err := requireFlag(flags, "revision")
	if err != nil {
		return err
	}
	if !revisionPattern.MatchString(revision) {
		return fmt.Errorf("revision must be a full 40- or 64-character lowercase hexadecimal SHA")
	}
	ttlValue, err := requireFlag(flags, "ttl-seconds")
	if err != nil {
		return err
	}
	ttl, err := strconv.Atoi(ttlValue)
	if err != nil || ttl < 1 || ttl > 86400 {
		return fmt.Errorf("TTL must be between 1 and 86400 seconds")
	}
	token, err := readSingleLine(stdin, maximumCredentialSize, "wrapping token")
	if err != nil {
		return err
	}
	if !tokenPattern.MatchString(token) {
		return fmt.Errorf("wrapping token contains invalid characters")
	}
	lock, err := p.lockApp(app)
	if err != nil {
		return err
	}
	defer unlockFile(lock)
	if _, err := p.State.LoadApp(app); err != nil {
		return fmt.Errorf("app integration is not enabled: %w", err)
	}
	if err := p.State.EnsureApp(app); err != nil {
		return err
	}
	_ = secureRemove(p.State.PendingTokenPath(app))
	_ = os.Remove(p.State.PendingMetadataPath(app))
	if err := writeFileAtomic(p.State.PendingTokenPath(app), []byte(token+"\n"), 0600); err != nil {
		return err
	}
	now := time.Now().UTC()
	metadata := StagedCredential{Revision: revision, StagedAt: now, ExpiresAt: now.Add(time.Duration(ttl) * time.Second)}
	if err := writeJSONAtomic(p.State.PendingMetadataPath(app), metadata, 0600); err != nil {
		_ = secureRemove(p.State.PendingTokenPath(app))
		return err
	}
	return nil
}

func secureRemove(path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return err
	}
	if info, statErr := file.Stat(); statErr == nil && info.Size() > 0 {
		zeros := make([]byte, info.Size())
		_, _ = file.WriteAt(zeros, 0)
		_ = file.Sync()
	}
	closeErr := file.Close()
	removeErr := os.Remove(path)
	if closeErr != nil {
		return closeErr
	}
	if removeErr != nil && !isNotExist(removeErr) {
		return removeErr
	}
	return nil
}
