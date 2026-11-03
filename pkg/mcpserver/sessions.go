package mcpserver

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TranscriptMessage is one stored line of an external session transcript.
type TranscriptMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
	TS      string `json:"ts"`
}

// ExternalSession describes a conversation an external agent had elsewhere.
// Transcripts are stored verbatim; only the summary is optional metadata.
type ExternalSession struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	Topic     string `json:"topic,omitempty"`
	Status    string `json:"status"`
	Summary   string `json:"summary,omitempty"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	Messages  int    `json:"messages"`
}

const (
	sessionStatusOpen   = "open"
	sessionStatusClosed = "closed"
)

// sanitizeName restricts path components to a small safe alphabet.
func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func newSessionID() string {
	// 8 random bytes (was 4): a same-second collision would silently
	// overwrite an existing session dir.
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(buf[:])
}

func sessionsRoot(workspace, agent string) string {
	return filepath.Join(workspace, "sessions-external", sanitizeName(agent))
}

func sessionDir(workspace, agent, id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || sanitizeName(id) != id {
		return "", fmt.Errorf("mcp: invalid session id %q", id)
	}
	safeAgent := sanitizeName(agent)
	if safeAgent == "" || safeAgent == "." || safeAgent == ".." {
		return "", fmt.Errorf("mcp: invalid agent name %q", agent)
	}
	dir := filepath.Join(sessionsRoot(workspace, safeAgent), id)
	if !strings.HasPrefix(dir, sessionsRoot(workspace, safeAgent)+string(filepath.Separator)) {
		return "", fmt.Errorf("mcp: session id %q escapes its agent directory", id)
	}
	return dir, nil
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	// Atomic replace: a crash mid-write must not leave a truncated meta.json
	// for the next reader.
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// withSessionLock serialises read-modify-write of one session directory
// across goroutines and processes (gateway + several MCP clients may touch
// the same session). Same reclaim-stale-lock pattern as pkg/fish/lock.go.
func withSessionLock(dir string, fn func() error) error {
	lockPath := filepath.Join(dir, ".lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			defer func() { _ = os.Remove(lockPath) }()
			return fn()
		}
		if !os.IsExist(err) {
			return err
		}
		if st, statErr := os.Stat(lockPath); statErr == nil && time.Since(st.ModTime()) > 60*time.Second {
			_ = os.Remove(lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mcp: timed out waiting for session lock in %s", dir)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func readMeta(dir string) (ExternalSession, error) {
	var meta ExternalSession
	raw, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("mcp: corrupt session meta in %s: %w", dir, err)
	}
	return meta, nil
}

// BeginSession creates a new external session owned by agent.
func BeginSession(workspace, agent, topic string) (ExternalSession, error) {
	meta := ExternalSession{
		ID:        newSessionID(),
		Agent:     agent,
		Topic:     strings.TrimSpace(topic),
		Status:    sessionStatusOpen,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	dir := filepath.Join(sessionsRoot(workspace, agent), meta.ID)
	if err := writeJSONFile(filepath.Join(dir, "meta.json"), meta); err != nil {
		return ExternalSession{}, err
	}
	return meta, nil
}

// AppendMessages appends verbatim transcript lines to an open session.
func AppendMessages(workspace, agent, id string, msgs []TranscriptMessage) (int, error) {
	dir, err := sessionDir(workspace, agent, id)
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, fmt.Errorf("mcp: messages must not be empty")
	}
	// Validate everything before touching disk: a bad message must not
	// leave a half-written transcript while meta stays unchanged.
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range msgs {
		if strings.TrimSpace(msgs[i].Role) == "" {
			return 0, fmt.Errorf("mcp: messages[%d] has no role", i)
		}
		if msgs[i].TS == "" {
			msgs[i].TS = now
		}
	}

	err = withSessionLock(dir, func() error {
		meta, err := readMeta(dir)
		if err != nil {
			return fmt.Errorf("mcp: session %q not found for agent %q", id, agent)
		}
		if meta.Status != sessionStatusOpen {
			return fmt.Errorf("mcp: session %q is already closed", id)
		}
		path := filepath.Join(dir, "transcript.jsonl")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		for i := range msgs {
			body, err := json.Marshal(msgs[i])
			if err != nil {
				return err
			}
			if _, err := f.Write(append(body, '\n')); err != nil {
				return err
			}
		}
		meta.Messages += len(msgs)
		return writeJSONFile(filepath.Join(dir, "meta.json"), meta)
	})
	if err != nil {
		return 0, err
	}
	return len(msgs), nil
}

// EndSession closes a session and stores its summary.
func EndSession(workspace, agent, id, summary string) (ExternalSession, error) {
	dir, err := sessionDir(workspace, agent, id)
	if err != nil {
		return ExternalSession{}, err
	}
	var meta ExternalSession
	err = withSessionLock(dir, func() error {
		m, err := readMeta(dir)
		if err != nil {
			return fmt.Errorf("mcp: session %q not found for agent %q", id, agent)
		}
		m.Status = sessionStatusClosed
		m.Summary = strings.TrimSpace(summary)
		m.EndedAt = time.Now().UTC().Format(time.RFC3339)
		if err := writeJSONFile(filepath.Join(dir, "meta.json"), m); err != nil {
			return err
		}
		meta = m
		return nil
	})
	if err != nil {
		return ExternalSession{}, err
	}
	return meta, nil
}

// ReadSession returns the session meta and its transcript. limit > 0 keeps
// only the last limit messages.
func ReadSession(workspace, agent, id string, limit int) (ExternalSession, []TranscriptMessage, error) {
	dir, err := sessionDir(workspace, agent, id)
	if err != nil {
		return ExternalSession{}, nil, err
	}
	meta, err := readMeta(dir)
	if err != nil {
		return ExternalSession{}, nil, fmt.Errorf("mcp: session %q not found for agent %q", id, agent)
	}

	var msgs []TranscriptMessage
	f, err := os.Open(filepath.Join(dir, "transcript.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return meta, []TranscriptMessage{}, nil
		}
		return meta, nil, err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var m TranscriptMessage
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		msgs = append(msgs, m)
	}
	if err := scanner.Err(); err != nil {
		return meta, nil, err
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	if msgs == nil {
		msgs = []TranscriptMessage{}
	}
	return meta, msgs, nil
}
