// Copyright 2026 Candace Labs

// Package bootstrap runs the bootstrap loop: the fixed-point pass that runs
// inside one batch job and turns any repository into an archive an earlier csf
// binary runs. It is the "ask C" slice of the laser_bootstrap program.
//
// The loop owns the fixed point and the keep/refuse decision. Its stages are
// function values registered in data (CS-6): the [NewLoop] constructor builds
// a pipeline of [Entry] values, and a sibling slice plugs a stage in by
// appending one [Entry] — mine, induce, lean, shell and the jev batch land that
// way without the loop changing. Each iteration measures the repository, runs
// the stages, measures again, and keeps the change only when it grew the seed
// without a refusal; otherwise the tree is rolled back and the refusal logged.
// It repeats until the reading stops changing, the cap is reached, or every
// chief row holds, then emits the archive.
//
// The loop has no network, no database and no subprocess: it reads and writes
// one directory tree, so it runs locally (no AWS) against a fixture
// repository, which is how its acceptance is proven.
package bootstrap
