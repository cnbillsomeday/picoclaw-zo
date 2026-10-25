package fish

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// appendJSONLine 把一条记录按 JSON Lines 追加到 path（自动建目录）。
// 写失败不影响主流程，返回的错误由调用方决定是否忽略。
func appendJSONLine(path string, value any) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	body, err := marshalJSON(value)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(body, '\n')); err != nil {
		return err
	}
	return nil
}

// writeJSONLines 原子地重写整个 JSON Lines 文件。
func writeJSONLines(path string, values []any) error {
	if err := ensureDir(filepath.Dir(path)); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, v := range values {
		body, err := marshalJSON(v)
		if err != nil {
			return err
		}
		buf.Write(body)
		buf.WriteByte('\n')
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// scanJSONLines 逐行读 JSON Lines，坏行和空行直接跳过。
func scanJSONLines(path string, each func(line []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := each(line); err != nil {
			continue
		}
	}
	return scanner.Err()
}

// marshalJSON 序列化但不转义 HTML 字符，方便人直接看文件。
func marshalJSON(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
