// Command memdiag reports the G2 remediation diagnostic that diag.sh collects.
//
// It is NOT a measurement tool. memstat computes equivalence-spec §3.6's
// figure from a cell of measured runs; this reads the SUT's own
// runtime/metrics at a small session count and prints per-session deltas for
// the runtime classes RFC-0001 §6.2's table is written in terms of, one row
// per configuration.
//
// Every number it prints is a runtime-internal point reading taken through the
// SUT's /introspect, not a cgroup median over a five-minute window, and no
// number here is a G2 baseline or may be presented as one.
//
//	go run ./cmd/memdiag -dir /path/to/diag/output
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// introspect is the subset of memsrv's report this tool reads.
type introspect struct {
	Observability            string `json:"observability"`
	Goroutines               int    `json:"goroutines"`
	MemoryClassesTotal       int64  `json:"memory_classes_total_bytes"`
	GCHeapLive               int64  `json:"gc_heap_live_bytes"`
	GCCycles                 int64  `json:"gc_cycles_total"`
	MemoryClassesHeapObjects int64  `json:"memory_classes_heap_objects_bytes"`
	MemoryClassesHeapStacks  int64  `json:"memory_classes_heap_stacks_bytes"`
	MemoryClassesHeapFree    int64  `json:"memory_classes_heap_free_bytes"`
	MemoryClassesHeapUnused  int64  `json:"memory_classes_heap_unused_bytes"`
}

type status struct {
	Live       int64 `json:"live"`
	Mounted    int64 `json:"mounted"`
	DialErrors int64 `json:"dial_errors"`
	ReadErrors int64 `json:"read_errors"`
	Acks       int64 `json:"acks"`
	Heartbeats int64 `json:"heartbeats"`
}

type record struct {
	GID          uint64   `json:"goroutine"`
	Copies       int      `json:"stack_copies_observed"`
	MoveSites    []string `json:"move_sites"`
	UsedBytes    int64    `json:"used_bytes_lower_bound"`
	Sites        []string `json:"sites"`
	DeepestSite  string   `json:"deepest_site"`
	DeepestStack string   `json:"deepest_stack"`
	Samples      int      `json:"samples"`
}

// cells are printed in this order, because the story reads in it.
var cells = []string{"off", "logger", "metrics", "tracer", "on", "on-quiet", "on-probe"}

func main() {
	dir := flag.String("dir", "", "directory diag.sh wrote")
	stacks := flag.Int("stack-frames", 24, "frames of the deepest stack to print")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "memdiag: -dir is required")
		os.Exit(2)
	}

	fmt.Println("G2 remediation diagnostic — runtime/metrics per session, NOT a §3.6 measurement")
	fmt.Println()
	fmt.Printf("%-9s %6s %7s %10s %10s %10s %10s %8s\n",
		"cell", "N", "gor/s", "stacks/s", "live/s", "objects/s", "total/s", "gc")
	fmt.Println(strings.Repeat("-", 78))

	for _, name := range cells {
		pre, err1 := readIntrospect(filepath.Join(*dir, "introspect-pre-"+name+".json"))
		post, err2 := readIntrospect(filepath.Join(*dir, "introspect-"+name+".json"))
		st, err3 := readStatus(filepath.Join(*dir, "status-"+name+".json"))
		if err1 != nil || err2 != nil || err3 != nil || st.Live == 0 {
			fmt.Printf("%-9s  (absent)\n", name)
			continue
		}
		n := float64(st.Live)
		fmt.Printf("%-9s %6d %7.2f %10.0f %10.0f %10.0f %10.0f %8d\n",
			name, st.Live,
			float64(post.Goroutines-pre.Goroutines)/n,
			float64(post.MemoryClassesHeapStacks-pre.MemoryClassesHeapStacks)/n,
			float64(post.GCHeapLive-pre.GCHeapLive)/n,
			float64(post.MemoryClassesHeapObjects-pre.MemoryClassesHeapObjects)/n,
			float64(post.MemoryClassesTotal-pre.MemoryClassesTotal)/n,
			post.GCCycles,
		)
	}

	fmt.Println()
	fmt.Println("forced-GC floor (after debug.FreeOSMemory), same per-session arithmetic:")
	fmt.Printf("%-9s %10s %10s %10s\n", "cell", "stacks/s", "live/s", "objects/s")
	fmt.Println(strings.Repeat("-", 42))
	for _, name := range cells {
		pre, err1 := readIntrospect(filepath.Join(*dir, "introspect-pre-"+name+".json"))
		floor, err2 := readIntrospect(filepath.Join(*dir, "floor-"+name+".json"))
		st, err3 := readStatus(filepath.Join(*dir, "status-"+name+".json"))
		if err1 != nil || err2 != nil || err3 != nil || st.Live == 0 {
			continue
		}
		n := float64(st.Live)
		fmt.Printf("%-9s %10.0f %10.0f %10.0f\n", name,
			float64(floor.MemoryClassesHeapStacks-pre.MemoryClassesHeapStacks)/n,
			float64(floor.GCHeapLive-pre.GCHeapLive)/n,
			float64(floor.MemoryClassesHeapObjects-pre.MemoryClassesHeapObjects)/n)
	}

	fmt.Println()
	fmt.Println("driver counters (a cell whose sessions died is not a cell):")
	for _, name := range cells {
		st, err := readStatus(filepath.Join(*dir, "status-"+name+".json"))
		if err != nil {
			continue
		}
		fmt.Printf("  %-9s live=%d mounted=%d dial_err=%d read_err=%d acks=%d hb=%d\n",
			name, st.Live, st.Mounted, st.DialErrors, st.ReadErrors, st.Acks, st.Heartbeats)
	}

	printProbe(filepath.Join(*dir, "stackprobe-on-probe.json"), *stacks)
}

func printProbe(path string, frames int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var recs []record
	if err := json.Unmarshal(raw, &recs); err != nil {
		fmt.Fprintln(os.Stderr, "memdiag: stackprobe:", err)
		return
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].Copies > recs[j].Copies })

	// Stack copies, by goroutine role. Every copy is one doubling, so this is
	// the doubling count g2-baseline.md §5.1 inferred from the size of the
	// number rather than observed.
	byRole := map[string][]int{}
	for _, r := range recs {
		role := roleOf(r.DeepestStack)
		if role == "" || role == "other" {
			continue
		}
		byRole[role] = append(byRole[role], r.Copies)
	}
	fmt.Println()
	fmt.Println("stack probe — observed stack RELOCATIONS per goroutine (each one is a doubling):")
	fmt.Printf("%-16s %8s %10s %10s %10s\n", "role", "n", "min", "median", "max")
	fmt.Println(strings.Repeat("-", 58))
	roles := make([]string, 0, len(byRole))
	for role := range byRole {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	for _, role := range roles {
		v := byRole[role]
		sort.Ints(v)
		fmt.Printf("%-16s %8d %10d %10d %10d\n", role, len(v), v[0], v[len(v)/2], v[len(v)-1])
	}

	fmt.Println()
	fmt.Printf("%-10s %7s %-34s %-30s %s\n", "goroutine", "copies", "move_sites", "deepest_site", "extent_in_final_epoch")
	fmt.Println(strings.Repeat("-", 110))
	for i, r := range recs {
		if i >= 8 {
			fmt.Printf("  … %d more goroutines\n", len(recs)-i)
			break
		}
		fmt.Printf("%-10d %7d %-34s %-30s %d\n", r.GID, r.Copies,
			strings.Join(r.MoveSites, ","), r.DeepestSite, r.UsedBytes)
	}

	// The deepest call path, once per distinct goroutine role. Two roles are
	// expected: the session actor and the connection read pump.
	seen := map[string]bool{}
	for _, r := range recs {
		role := roleOf(r.DeepestStack)
		if seen[role] || role == "" {
			continue
		}
		seen[role] = true
		fmt.Println()
		fmt.Printf("=== deepest observed path, role %s (goroutine %d, extent %d B, site %s)\n",
			role, r.GID, r.UsedBytes, r.DeepestSite)
		lines := strings.Split(r.DeepestStack, "\n")
		if len(lines) > frames*2 {
			lines = lines[:frames*2]
		}
		fmt.Println(strings.Join(lines, "\n"))
	}
}

// roleOf names a goroutine by the outermost gotth-live frame in its dump.
func roleOf(dump string) string {
	switch {
	case strings.Contains(dump, "session.(*Actor).Run"):
		return "session-actor"
	case strings.Contains(dump, "wsx.(*conn).readPump"), strings.Contains(dump, "wsx.(*Handler).ServeHTTP"):
		return "read-pump"
	case dump == "":
		return ""
	}
	return "other"
}

func readIntrospect(path string) (introspect, error) {
	var v introspect
	raw, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v)
}

func readStatus(path string) (status, error) {
	var v status
	raw, err := os.ReadFile(path)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(raw, &v)
}
