package analyze

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"howett.net/plist"

	"github.com/IGoryunov/macsweep/internal/rules"
)

// Downloads triages the top level of ~/Downloads by age and file class.
type Downloads struct{}

// Name implements Analyzer.
func (Downloads) Name() string { return "Downloads" }

// DownloadsMinSize hides small documents from the list.
const DownloadsMinSize = 10 << 20

var installerExt = map[string]bool{".dmg": true, ".pkg": true, ".mpkg": true, ".iso": true, ".xip": true, ".app": true}
var partialExt = map[string]bool{".part": true, ".crdownload": true, ".download": true, ".partial": true}
var archiveExt = map[string]bool{".zip": true, ".tar": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true}

// Analyze implements Analyzer.
func (d Downloads) Analyze(ctx context.Context, env Env) ([]Finding, []string) {
	dir := filepath.Join(env.Home, "Downloads")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil
	}
	now := env.now()
	var out []Finding
	for _, e := range ents {
		if ctx.Err() != nil {
			break
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		p := filepath.Join(dir, name)
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		when, source := downloadTime(p, fi)
		age := int(now.Sub(when).Hours() / 24)
		ext := strings.ToLower(filepath.Ext(name))
		class, verdict := "file", rules.Review
		switch {
		case partialExt[ext]:
			class, verdict = "unfinished download", rules.Safe
		case installerExt[ext]:
			class = "installer"
			if age >= 30 {
				verdict = rules.Safe
			}
		case archiveExt[ext]:
			class = "archive"
		case fi.IsDir():
			class = "folder"
		}
		reason := fmt.Sprintf("downloaded %d days ago", age)
		if source != "" {
			reason += " from " + source
		}
		note := class + " in Downloads"
		recovery := "look at it before deciding; it is in your Downloads folder"
		if class == "installer" {
			recovery = "installers can be downloaded again" + map[bool]string{true: " from " + source, false: ""}[source != ""]
		}
		if class == "unfinished download" {
			recovery = "an interrupted download that cannot be resumed; download the file again"
		}
		out = append(out, Finding{
			Path:    p,
			Rule:    rules.Rule{ID: "analyze/downloads/" + class, Group: d.Name(), Verdict: verdict, Note: note, Recovery: recovery, MinSize: DownloadsMinSize},
			Reasons: []string{reason, "age bucket: " + bucket(age)},
			Tags:    map[string]string{"class": class, "age_days": strconv.Itoa(age), "age_bucket": bucket(age), "source": source, "downloaded": when.Format("2006-01-02")},
		})
	}
	return out, nil
}

func bucket(days int) string {
	switch {
	case days < 30:
		return "< 30 days"
	case days < 90:
		return "30–90 days"
	case days < 180:
		return "90–180 days"
	case days < 365:
		return "180–365 days"
	}
	return "> 1 year"
}

// downloadTime returns when the item arrived: the quarantine timestamp if the
// file was downloaded by a browser, else its birth time, else mtime. It also
// returns the source host from kMDItemWhereFroms when present.
func downloadTime(p string, fi os.FileInfo) (time.Time, string) {
	when := fi.ModTime()
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		if b := time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec); b.Unix() > 0 && b.Before(when) {
			when = b
		}
	}
	if q := xattr(p, "com.apple.quarantine"); q != "" {
		// format: flags;hex-seconds;agent;uuid
		if f := strings.Split(q, ";"); len(f) >= 2 {
			if secs, err := strconv.ParseInt(f[1], 16, 64); err == nil && secs > 0 {
				when = time.Unix(secs, 0)
			}
		}
	}
	source := ""
	if raw := xattr(p, "com.apple.metadata:kMDItemWhereFroms"); raw != "" {
		var urls []string
		if _, err := plist.Unmarshal([]byte(raw), &urls); err == nil && len(urls) > 0 {
			if u, err := url.Parse(urls[0]); err == nil && u.Host != "" {
				source = u.Host
			}
		}
	}
	return when, source
}

func xattr(p, name string) string {
	buf := make([]byte, 4096)
	n, err := unix.Getxattr(p, name, buf)
	if err != nil || n <= 0 {
		return ""
	}
	return string(buf[:n])
}
