// Command memstat turns the sample files measure.sh collects into the figure
// equivalence-spec §3.6 defines, and refuses to produce one from a window that
// is not §3.6's window.
//
// It is a separate step from the sampler on purpose. The sampler is a shell
// loop on the host, because M(x) is read from OUTSIDE the measured container
// and the files are under /sys/fs/cgroup; the arithmetic is here, in Go, where
// it has tests. Nothing computes a median in bash.
//
// Usage:
//
//	memstat -run <rundir>          # one run: M(0), M(N), (M(N)−M(0))/N
//	memstat -cell <celldir>        # every run under a cell directory, pooled
//
// A run directory holds m0.csv, mn.csv and run.json. A cell directory holds run
// directories. Output is JSON on stdout; a human-readable summary goes to
// stderr so a pipe carries only the data.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/candacelabs/csf/pkg/gotth/test/memory"
)

// runMeta is the part of a run's manifest memstat needs. measure.sh writes the
// whole manifest; this reads the fields the arithmetic depends on and ignores
// the rest, so adding a field to the manifest never breaks the report.
type runMeta struct {
	RunID string `json:"run_id"`
	N     int    `json:"n"`
	Order string `json:"window_order"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "memstat:", err)
		os.Exit(1)
	}
}

func run() error {
	runDir := flag.String("run", "", "a single run directory holding m0.csv, mn.csv and run.json")
	cellDir := flag.String("cell", "", "a directory of run directories, pooled into one cell")
	flag.Parse()

	switch {
	case *runDir != "" && *cellDir != "":
		return errors.New("give -run or -cell, not both")
	case *runDir != "":
		r, err := readRun(*runDir)
		if err != nil {
			return err
		}
		describeRun(os.Stderr, r)
		return emit(r)
	case *cellDir != "":
		cell, err := readCell(*cellDir)
		if err != nil {
			return err
		}
		for _, r := range cell.Runs {
			describeRun(os.Stderr, r)
		}
		fmt.Fprintf(os.Stderr,
			"\npooled over %d run(s) at N=%d: %.1f B/session (min %.1f, max %.1f, spread %.1f %%)%s\n",
			len(cell.Runs), cell.N, cell.PooledPerSession, cell.MinPerSession, cell.MaxPerSession,
			cell.Spread*100, unstableNote(cell.Unstable))
		return emit(cell)
	default:
		flag.Usage()
		return errors.New("nothing to do")
	}
}

func unstableNote(unstable bool) string {
	if unstable {
		return "  ** UNSTABLE by §6's 20 % rule: re-collect the whole cell, and publish this set anyway **"
	}
	return ""
}

func readRun(dir string) (memory.Run, error) {
	var meta runMeta
	raw, err := os.ReadFile(filepath.Join(dir, "run.json"))
	if err != nil {
		return memory.Run{}, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return memory.Run{}, fmt.Errorf("%s/run.json: %w", dir, err)
	}
	if meta.RunID == "" {
		meta.RunID = filepath.Base(dir)
	}

	m0, err := readWindow(filepath.Join(dir, "m0.csv"))
	if err != nil {
		return memory.Run{}, err
	}
	mn, err := readWindow(filepath.Join(dir, "mn.csv"))
	if err != nil {
		return memory.Run{}, err
	}

	medianM0, err := memory.Median(m0)
	if err != nil {
		return memory.Run{}, err
	}
	medianMN, err := memory.Median(mn)
	if err != nil {
		return memory.Run{}, err
	}
	perSession, err := memory.PerSession(medianM0, medianMN, meta.N)
	if err != nil {
		return memory.Run{}, err
	}

	return memory.Run{
		ID: meta.RunID, N: meta.N, Order: meta.Order,
		M0: medianM0, MN: medianMN,
		M0Samples: len(m0), MNSamples: len(mn),
		PerSession: perSession,
	}, nil
}

// readWindow loads a sample file and holds it to §3.6's shape before any
// arithmetic touches it. A window that is not 60 samples at 1 Hz is a failed
// run, and a failed run is reported as failed rather than medianed.
func readWindow(path string) ([]memory.Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only

	samples, err := memory.ParseCSV(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := memory.CheckWindow(samples); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return samples, nil
}

func readCell(dir string) (memory.Cell, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return memory.Cell{}, err
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), "run.json")); err == nil {
			dirs = append(dirs, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(dirs)
	if len(dirs) == 0 {
		return memory.Cell{}, fmt.Errorf("%s holds no run directories", dir)
	}

	runs := make([]memory.Run, 0, len(dirs))
	for _, d := range dirs {
		r, err := readRun(d)
		if err != nil {
			return memory.Cell{}, err
		}
		runs = append(runs, r)
	}
	return memory.Summarize(runs)
}

func describeRun(w *os.File, r memory.Run) {
	fmt.Fprintf(w,
		"%-24s N=%-5d order=%-7s M(0)=%12.1f B  M(N)=%12.1f B  Δ=%12.1f B  ⇒ %9.1f B/session\n",
		r.ID, r.N, r.Order, r.M0, r.MN, r.MN-r.M0, r.PerSession)
}

func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
