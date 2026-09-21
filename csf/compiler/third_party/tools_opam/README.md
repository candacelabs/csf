The pinned upstream beta predates Bazel 9. This patch:

- enables external C++ rule autoloading in its nested Bazel invocation;
- keeps that invocation on the same patched module and a fixed cache path;
- disables opam's bubblewrap sandbox inside the already isolated build container;
- preserves the compiler version returned when creating a fresh switch;
- materializes locked toolchains through a bootstrap repository dependency,
  so a restored extension lock cannot skip compiler/package installation;
- resolves the complete opam lock together and invalidates package imports
  when their version changes;
- excludes a package-named executable from `exports_files`, preventing its
  collision with the generated OCaml library target.

Direct package versions are declared in `MODULE.bazel`; the generated
`deploy/home/opam.lock.json` records schema version 1, exact `packages` versions,
and the upstream discovery result as `libraries`. OPAM package names and findlib
library names can differ, so package installation consumes `packages` while
repository declarations consume `libraries`. Normal builds install every locked
version and reject drift in either list. Update direct pins, then
run `deploy/home/update-opam-lock.sh` to explicitly refresh that graph and review
the resulting lock diff. `MODULE.bazel.lock` separately owns Bazel resolution.

For locked XDG toolchains, extension evaluation only declares repositories.
`opam.bootstrap` owns the OPAM executable, compiler, installed packages, and
configuration helper inside its repository directory. Package repositories read
its manifest label before generating their imports, and `opam.lock` reads its
resolved graph. A strict build from empty home/output caches therefore follows
the same dependency path as a cached build.
