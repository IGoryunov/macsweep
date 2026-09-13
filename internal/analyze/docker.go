package analyze

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/IGoryunov/macsweep/internal/rules"
)

// Docker advises on unused images, stopped containers, dangling volumes and
// build cache. It never removes anything itself: items are keep candidates
// with the exact command as recovery; `macsweep docker prune` runs them.
type Docker struct{}

// Name implements Analyzer.
func (Docker) Name() string { return "Docker (advisor)" }

// DockerItem is one removable Docker object.
type DockerItem struct {
	Kind    string `json:"kind"` // image | container | volume | build-cache
	ID      string `json:"id"`
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Detail  string `json:"detail"`
	Command string `json:"command"`
	Args    []string
}

type dockerImage struct {
	Repository, Tag, ID, CreatedSince, Size string
}
type dockerContainer struct {
	ID, Image, Names, Status, State, Size string
}
type dockerVolume struct {
	Name, Driver string
}
type dockerDF struct {
	Type, TotalCount, Active, Size, Reclaimable string
}

// Analyze implements Analyzer.
func (d Docker) Analyze(ctx context.Context, env Env) ([]Finding, []string) {
	items, warn := DockerItems(ctx, env)
	if warn != "" {
		return nil, []string{warn}
	}
	var out []Finding
	for _, it := range items {
		out = append(out, Finding{
			Path:    "docker://" + it.Kind + "/" + it.ID,
			Display: it.Kind + " " + it.Name,
			Bytes:   it.Bytes,
			Rule: rules.Rule{ID: "analyze/docker/" + it.Kind, Group: d.Name(), Verdict: rules.Keep,
				Note:     it.Detail,
				Recovery: "not reversible; remove with: " + it.Command + "  (or macsweep docker prune)"},
			Reasons: []string{"Docker objects have no Trash; macsweep only advises here"},
			Tags:    map[string]string{"kind": it.Kind, "command": it.Command, "id": it.ID},
		})
	}
	return out, nil
}

// DockerItems queries the Docker CLI. The warning is non-empty when Docker is
// unavailable, in which case no items are returned.
func DockerItems(ctx context.Context, env Env) ([]DockerItem, string) {
	run := func(args ...string) ([]string, error) {
		out, err := env.exec(ctx, "docker", args...)
		if err != nil {
			return nil, err
		}
		var lines []string
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			if l := strings.TrimSpace(sc.Text()); l != "" {
				lines = append(lines, l)
			}
		}
		return lines, nil
	}
	imgLines, err := run("images", "--format", "{{json .}}")
	if err != nil {
		return nil, "Docker analysis skipped: docker CLI failed (is Docker Desktop running?): " + shortErr(err)
	}
	psLines, err := run("ps", "-a", "--format", "{{json .}}")
	if err != nil {
		return nil, "Docker analysis skipped: " + shortErr(err)
	}
	volLines, _ := run("volume", "ls", "-f", "dangling=true", "--format", "{{json .}}")
	dfLines, _ := run("system", "df", "--format", "{{json .}}")

	used := map[string]bool{}
	var items []DockerItem
	for _, l := range psLines {
		var c dockerContainer
		if json.Unmarshal([]byte(l), &c) != nil {
			continue
		}
		used[c.Image] = true
		used[strings.TrimPrefix(c.Image, "sha256:")] = true
		state := strings.ToLower(c.State)
		if state == "exited" || state == "created" || state == "dead" {
			items = append(items, DockerItem{Kind: "container", ID: c.ID, Name: c.Names, Bytes: parseDockerSize(firstSize(c.Size)),
				Detail: fmt.Sprintf("stopped container %s from image %s (%s)", c.Names, c.Image, c.Status), Command: "docker rm " + c.ID, Args: []string{"rm", c.ID}})
		}
	}
	for _, l := range imgLines {
		var im dockerImage
		if json.Unmarshal([]byte(l), &im) != nil {
			continue
		}
		ref := im.Repository + ":" + im.Tag
		dangling := im.Repository == "<none>" || im.Tag == "<none>"
		inUse := used[ref] || used[im.ID] || used[im.Repository]
		for u := range used {
			if strings.HasPrefix(im.ID, u) || strings.HasPrefix(u, im.ID) {
				inUse = true
			}
		}
		if inUse {
			continue
		}
		it := DockerItem{Kind: "image", ID: im.ID, Bytes: parseDockerSize(im.Size)}
		if dangling {
			it.Name = "<none> " + im.ID
			it.Detail = fmt.Sprintf("dangling image layer, created %s", im.CreatedSince)
			it.Command, it.Args = "docker rmi "+im.ID, []string{"rmi", im.ID}
		} else {
			it.Name = ref
			it.Detail = fmt.Sprintf("image not used by any container, created %s", im.CreatedSince)
			it.Command, it.Args = "docker rmi "+ref, []string{"rmi", ref}
		}
		items = append(items, it)
	}
	for _, l := range volLines {
		var v dockerVolume
		if json.Unmarshal([]byte(l), &v) != nil || v.Name == "" {
			continue
		}
		items = append(items, DockerItem{Kind: "volume", ID: v.Name, Name: v.Name, Detail: "volume not attached to any container", Command: "docker volume rm " + v.Name, Args: []string{"volume", "rm", v.Name}})
	}
	for _, l := range dfLines {
		var df dockerDF
		if json.Unmarshal([]byte(l), &df) != nil {
			continue
		}
		if strings.EqualFold(df.Type, "Build Cache") {
			if b := parseDockerSize(firstSize(df.Reclaimable)); b > 0 {
				items = append(items, DockerItem{Kind: "build-cache", ID: "all", Name: "build cache", Bytes: b, Detail: "reclaimable build cache (" + df.Reclaimable + ")", Command: "docker builder prune -f", Args: []string{"builder", "prune", "-f"}})
			}
		}
	}
	return items, ""
}

func shortErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i > 0 {
		s = s[:i]
	}
	return s
}

// firstSize extracts the leading size from strings like "1.2GB (virtual 3GB)" or "2.1GB (55%)".
func firstSize(s string) string {
	if i := strings.Index(s, " ("); i > 0 {
		return s[:i]
	}
	return s
}

var dockerSizeRe = regexp.MustCompile(`^([0-9.]+)\s*([kKMGT]?i?B)$`)

// parseDockerSize parses Docker's decimal human sizes ("1.23GB", "512kB", "0B").
func parseDockerSize(s string) int64 {
	m := dockerSizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	f, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	unit := strings.ToLower(m[2])
	mult := map[string]float64{"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12, "kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40}[unit]
	return int64(math.Round(f * mult))
}
