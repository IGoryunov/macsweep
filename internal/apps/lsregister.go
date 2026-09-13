package apps

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const lsregisterPath = "/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"

func fromLSRegister(ctx context.Context, roots []string) (*Index, error) {
	if _, err := os.Stat(lsregisterPath); err != nil {
		return nil, fmt.Errorf("lsregister: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, lsregisterPath, "-dump")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("lsregister: %w", err)
	}
	idx := parseLSRegister(out, roots)
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("lsregister: %w", err)
	}
	return idx, nil
}

var trailingHex = regexp.MustCompile(`\s*\(0x[0-9a-fA-F]+\)\s*$`)

// parseLSRegister extracts app bundles under roots from `lsregister -dump`
// output. Each record has a "path:" line followed later by "identifier:".
func parseLSRegister(r io.Reader, roots []string) *Index {
	idx := newIndex("lsregister")
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var curPath, curTeam, curID string
	flush := func() {
		if curPath != "" && strings.HasSuffix(curPath, ".app") && underAny(curPath, roots) {
			idx.addWithTeam(curPath, curID, curTeam)
		}
		curPath, curTeam, curID = "", "", ""
	}
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "path:"):
			flush()
			curPath = trailingHex.ReplaceAllString(strings.TrimSpace(strings.TrimPrefix(line, "path:")), "")
		case strings.HasPrefix(line, "identifier:"):
			curID = strings.TrimSpace(strings.TrimPrefix(line, "identifier:"))
		case strings.HasPrefix(line, "teamID:"):
			curTeam = strings.TrimSpace(strings.TrimPrefix(line, "teamID:"))
		case strings.HasPrefix(line, "----"):
			flush()
		}
	}
	flush()
	return idx
}
