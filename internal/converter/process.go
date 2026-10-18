package converter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type commandResult struct {
	stdout          []byte
	stderr          []byte
	stdoutTruncated bool
	stderrTruncated bool
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit < 0 {
		return b.buffer.Write(p)
	}
	remaining := b.limit - int64(b.buffer.Len())
	if remaining > 0 {
		toWrite := int64(len(p))
		if toWrite > remaining {
			toWrite = remaining
		}
		_, _ = b.buffer.Write(p[:toWrite])
	}
	if int64(len(p)) > remaining {
		b.truncated = true
	}
	return len(p), nil
}

func (c *Converter) runCommand(ctx context.Context, name string, args ...string) (commandResult, error) {
	var result commandResult
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := &boundedBuffer{limit: c.options.MaxToolOutputBytes}
	stderr := &boundedBuffer{limit: minInt64(c.options.MaxToolOutputBytes, 1<<20)}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	result.stdout = stdout.buffer.Bytes()
	result.stderr = stderr.buffer.Bytes()
	result.stdoutTruncated = stdout.truncated
	result.stderrTruncated = stderr.truncated
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		return result, err
	}
	if stdout.truncated {
		return result, newConversionError(ErrQuotaExceeded, name, errors.New("tool standard output exceeded configured limit"))
	}
	return result, nil
}

func commandError(name string, err error, result commandResult) error {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("%s: %w", name, err)
	}
	message := strings.TrimSpace(string(result.stderr))
	if message == "" {
		message = strings.TrimSpace(string(result.stdout))
	}
	if len(message) > 4096 {
		message = message[:4096]
	}
	if message == "" {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Errorf("%s: %w: %s", name, err, message)
}

func warningsFromCommand(tool string, output []byte, page int) []Warning {
	var warnings []Warning
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		code := ""
		switch {
		case strings.Contains(lower, "font") && (strings.Contains(lower, "substitut") || strings.Contains(lower, "missing") || strings.Contains(lower, "not found")):
			code = "font_substitution"
		case strings.Contains(lower, "syntax warning") || strings.Contains(lower, "syntax error"):
			code = "pdf_syntax"
		case strings.Contains(lower, "warning") || strings.Contains(lower, "unknown") || strings.Contains(lower, "unsupported"):
			code = tool + "_warning"
		}
		if code == "" {
			continue
		}
		if len(line) > 1000 {
			line = line[:1000]
		}
		warnings = append(warnings, Warning{Code: code, Message: line, Page: page})
	}
	return warnings
}

func copyFileAtomic(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("source is not a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return 0, err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".copy-*")
	if err != nil {
		return 0, err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	written, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr != nil {
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if written != info.Size() {
		return 0, io.ErrUnexpectedEOF
	}
	if err := os.Chmod(tmp, 0644); err != nil {
		return 0, err
	}
	return written, replaceFile(tmp, dst)
}

func replaceFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if runtime.GOOS != "windows" {
		return err
	} else {
		if removeErr := os.Remove(dst); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(src, dst)
	}
}

func minInt64(a, b int64) int64 {
	if a < 0 || b < 0 {
		return -1
	}
	if a < b {
		return a
	}
	return b
}
