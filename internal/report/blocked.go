package report

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/jancuk/semar/internal/domain"
)

// maxMetShown is how many scenarios a blocked host names before the rest is a count.
const maxMetShown = 3

// BlockedHost is a host that the browser refused to reach, with the scenarios, or the criteria of a
// plan, that met it.
type BlockedHost struct {
	Host string
	Met  []string
}

// BlockedHosts lists the hosts that the browser blocked in the web actions of a run, sorted, each
// with the scenarios that met it, sorted and without repeats.
func BlockedHosts(acts []domain.Activity) []BlockedHost {
	var out []BlockedHost
	for _, a := range acts {
		if a.Surface != domain.SurfaceWeb {
			continue
		}
		for _, h := range a.Blocked {
			out = AddBlocked(out, h, string(a.Use.Scenario))
		}
	}
	return out
}

// AddBlocked notes that met ran into host. The list stays sorted by host, and each host keeps its
// names sorted and without repeats. A name that is empty is left out.
func AddBlocked(list []BlockedHost, host, met string) []BlockedHost {
	i, found := slices.BinarySearchFunc(list, host, func(b BlockedHost, h string) int { return strings.Compare(b.Host, h) })
	if !found {
		list = slices.Insert(list, i, BlockedHost{Host: host})
	}
	if met == "" {
		return list
	}
	j, seen := slices.BinarySearch(list[i].Met, met)
	if !seen {
		list[i].Met = slices.Insert(list[i].Met, j, met)
	}
	return list
}

// metText names the first few and counts the rest, as in "A, B, C and 2 more scenarios".
func metText(met []string, noun string) string {
	if len(met) <= maxMetShown {
		return strings.Join(met, ", ")
	}
	return fmt.Sprintf("%s and %d more %ss", strings.Join(met[:maxMetShown], ", "), len(met)-maxMetShown, noun)
}

func hostCount(n int) string {
	if n == 1 {
		return "1 host"
	}
	return fmt.Sprintf("%d hosts", n)
}

// WriteBlocked prints the hosts that the browser blocked, who met them, why it matters, and what to
// do. noun is what met the hosts: scenario in a run, criterion in a plan. It prints nothing when
// there is no host.
func WriteBlocked(w io.Writer, env, noun string, hosts []BlockedHost) {
	if len(hosts) == 0 {
		return
	}
	fmt.Fprintf(w, "blocked hosts: the browser did not reach %s, because environments.%s.hosts does not list them\n", hostCount(len(hosts)), plain(env))
	for _, h := range hosts {
		line := "  " + plain(h.Host)
		if len(h.Met) > 0 {
			line += ": " + plain(metText(h.Met, noun))
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "a page that misses data from a blocked host can pass or fail for the wrong reason; no outcome changed. To allow them, add the host under environments.%s.hosts in .semar/semar.yaml, or run semar login, which offers to add them\n", plain(env))
}

// blockedView is the section of the report about blocked hosts.
type blockedView struct{ Host, Met string }

func blockedViews(acts []domain.Activity) []blockedView {
	var out []blockedView
	for _, h := range BlockedHosts(acts) {
		out = append(out, blockedView{Host: shown(h.Host), Met: shown(metText(h.Met, "scenario"))})
	}
	return out
}
