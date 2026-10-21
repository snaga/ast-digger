package impact

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// GetGitDiff extracts file diff information comparing baseRef and current working tree,
// including untracked files, staged and unstaged changes.
// If baseRef is empty, "HEAD" is used as default.
func GetGitDiff(projectRoot string, baseRef string) ([]*FileDiffInfo, error) {
	if baseRef == "" {
		baseRef = "HEAD"
	}

	// 1. Run git diff -M <baseRef> to get working tree differences against baseRef
	diffOut, err := runGit(projectRoot, "-c", "core.quotepath=false", "diff", "-M", baseRef)
	if err != nil {
		return nil, fmt.Errorf("failed to run git diff against %s: %w", baseRef, err)
	}

	diffInfos, err := parseGitDiff(diffOut)
	if err != nil {
		return nil, fmt.Errorf("failed to parse git diff: %w", err)
	}

	existingFiles := make(map[string]bool)
	for _, info := range diffInfos {
		existingFiles[info.FilePath] = true

		// Read OldContent from git show <baseRef>:<oldPath> if not a new file
		if !info.IsNew {
			targetOldPath := info.FilePath
			if info.OldPath != "" {
				targetOldPath = info.OldPath
			}
			oldContent, err := runGit(projectRoot, "-c", "core.quotepath=false", "show", fmt.Sprintf("%s:%s", baseRef, targetOldPath))
			if err != nil {
				return nil, fmt.Errorf("failed to get old content for %s at %s: %w", targetOldPath, baseRef, err)
			}
			info.OldContent = oldContent
		}

		// Read NewContent from disk if not deleted
		if !info.IsDeleted {
			diskPath := filepath.Join(projectRoot, filepath.FromSlash(info.FilePath))
			newContent, err := os.ReadFile(diskPath)
			if err != nil {
				return nil, fmt.Errorf("failed to read modified file %s: %w", info.FilePath, err)
			}
			info.NewContent = newContent
		}
	}

	// 2. Run git status --porcelain -uall to discover untracked files
	statusOut, err := runGit(projectRoot, "-c", "core.quotepath=false", "status", "--porcelain", "-uall")
	if err != nil {
		return nil, fmt.Errorf("failed to run git status: %w", err)
	}

	for _, line := range strings.Split(string(statusOut), "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		rawPath := strings.TrimPrefix(line, "?? ")
		relPath := cleanGitStatusPath(rawPath)
		if relPath == "" || existingFiles[relPath] {
			continue
		}

		fullPath := filepath.Join(projectRoot, filepath.FromSlash(relPath))
		fi, err := os.Stat(fullPath)
		if err != nil || fi.IsDir() {
			continue
		}

		content, err := os.ReadFile(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read untracked file %s: %w", relPath, err)
		}

		lineCount := countLines(content)
		addedLines := make([]int, lineCount)
		for i := 0; i < lineCount; i++ {
			addedLines[i] = i + 1
		}

		untrackedInfo := &FileDiffInfo{
			FilePath:     relPath,
			IsNew:        true,
			IsDeleted:    false,
			AddedLines:   addedLines,
			DeletedLines: []int{},
			OldContent:   nil,
			NewContent:   content,
		}
		diffInfos = append(diffInfos, untrackedInfo)
		existingFiles[relPath] = true
	}

	// Sort results deterministically by FilePath
	sort.Slice(diffInfos, func(i, j int) bool {
		return diffInfos[i].FilePath < diffInfos[j].FilePath
	})

	return diffInfos, nil
}

// runGit executes a git command in the specified directory.
func runGit(projectRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = projectRoot
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git %s failed: %w (stderr: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// parseGitDiff parses the unified diff output from git diff.
func parseGitDiff(output []byte) ([]*FileDiffInfo, error) {
	var results []*FileDiffInfo
	var current *FileDiffInfo

	scanner := bufio.NewScanner(bytes.NewReader(output))
	const maxCapacity = 10 * 1024 * 1024
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, maxCapacity)

	inHunk := false
	currOld := 0
	currNew := 0

	finishCurrent := func() {
		if current == nil {
			return
		}
		// If FilePath is empty (e.g. deleted file where only --- was parsed), use OldPath
		if current.FilePath == "" && current.OldPath != "" {
			current.FilePath = current.OldPath
		}
		if current.FilePath != "" {
			if current.AddedLines == nil {
				current.AddedLines = []int{}
			}
			if current.DeletedLines == nil {
				current.DeletedLines = []int{}
			}
			results = append(results, current)
		}
		current = nil
		inHunk = false
	}

	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")

		if strings.HasPrefix(line, "diff --git ") {
			finishCurrent()
			current = &FileDiffInfo{
				AddedLines:   []int{},
				DeletedLines: []int{},
			}
			oldP, newP := parseDiffGitLine(line)
			if oldP != "" {
				current.OldPath = oldP
			}
			if newP != "" {
				current.FilePath = newP
			}
			continue
		}

		if current == nil {
			continue
		}

		if strings.HasPrefix(line, "new file mode ") {
			current.IsNew = true
			continue
		}
		if strings.HasPrefix(line, "deleted file mode ") {
			current.IsDeleted = true
			continue
		}
		if strings.HasPrefix(line, "rename from ") {
			current.OldPath = cleanGitDiffPath(strings.TrimPrefix(line, "rename from "))
			continue
		}
		if strings.HasPrefix(line, "rename to ") {
			current.FilePath = cleanGitDiffPath(strings.TrimPrefix(line, "rename to "))
			continue
		}
		if line == "--- /dev/null" {
			current.IsNew = true
			continue
		}
		if strings.HasPrefix(line, "--- ") {
			p := cleanGitDiffPath(strings.TrimPrefix(line, "--- "))
			if p != "" {
				current.OldPath = p
			}
			continue
		}
		if line == "+++ /dev/null" {
			current.IsDeleted = true
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			p := cleanGitDiffPath(strings.TrimPrefix(line, "+++ "))
			if p != "" {
				current.FilePath = p
			}
			continue
		}

		if strings.HasPrefix(line, "@@ ") {
			match := hunkHeaderRegex.FindStringSubmatch(line)
			if match != nil {
				inHunk = true
				oldStart, _ := strconv.Atoi(match[1])
				newStart, _ := strconv.Atoi(match[3])
				currOld = oldStart
				currNew = newStart
			} else {
				inHunk = false
			}
			continue
		}

		if inHunk {
			if len(line) == 0 {
				continue
			}
			switch line[0] {
			case '+':
				current.AddedLines = append(current.AddedLines, currNew)
				currNew++
			case '-':
				current.DeletedLines = append(current.DeletedLines, currOld)
				currOld++
			case ' ':
				currOld++
				currNew++
			case '\\':
				// \ No newline at end of file - line count does not change
			default:
				// Line does not belong to hunk
				inHunk = false
			}
		}
	}

	finishCurrent()

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

// cleanGitDiffPath cleans path strings from git diff output.
func cleanGitDiffPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = unquoted
		}
	}
	if strings.HasPrefix(raw, "a/") || strings.HasPrefix(raw, "b/") {
		raw = raw[2:]
	}
	return filepath.ToSlash(raw)
}

// cleanGitStatusPath cleans path strings from git status output.
func cleanGitStatusPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		if unquoted, err := strconv.Unquote(raw); err == nil {
			raw = unquoted
		}
	}
	return filepath.ToSlash(raw)
}

// parseDiffGitLine extracts old and new paths from a "diff --git a/... b/..." line.
func parseDiffGitLine(line string) (oldPath, newPath string) {
	line = strings.TrimPrefix(line, "diff --git ")
	line = strings.TrimSpace(line)

	idx := strings.Index(line, " b/")
	if idx == -1 {
		idx = strings.Index(line, " \"b/")
	}
	if idx != -1 {
		oldPart := strings.TrimSpace(line[:idx])
		newPart := strings.TrimSpace(line[idx+1:])
		return cleanGitDiffPath(oldPart), cleanGitDiffPath(newPart)
	}
	return "", ""
}

// countLines counts the number of lines in a byte slice.
func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	lines := 0
	for _, b := range content {
		if b == '\n' {
			lines++
		}
	}
	if content[len(content)-1] != '\n' {
		lines++
	}
	return lines
}
