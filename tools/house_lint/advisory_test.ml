let expect label condition = if not condition then failwith label

let findings ?(path="fixture/source.go") rule source =
  let file = Source.parse path source in
  if Source.Node.has_error file.root then begin
    Printf.eprintf "Invalid fixture:\n%s\n" source;
    Source.descendants file.root |> List.iter (fun node ->
      if Source.Node.is_error node || Source.Node.is_missing node then
        Printf.eprintf "invalid fixture node %s: %s\n" (Source.kind node) (Source.text file node));
    failwith (path ^ ": invalid fixture syntax")
  end;
  Advisory.collect [file] |> List.filter (fun (finding : Source.finding) -> finding.rule = rule)

let count ?path label rule expected source =
  let actual = findings ?path rule source in
  if List.length actual <> expected then begin
    List.iter (fun (finding : Source.finding) -> Printf.eprintf "%s:%d %s %s\n" finding.path finding.line finding.rule finding.message) actual;
    failwith (Printf.sprintf "%s: expected %d findings, got %d" label expected (List.length actual))
  end

let test_mutexes () =
  count "mutex declarations include fields, embedded pointers and var initializers" "CS-5" 8 {|package fixture
import "sync"
type worker struct {
  guard sync.Mutex
  sync.RWMutex
  pointer *sync.Mutex
  table map[string]sync.Mutex
  nested struct { guard sync.Mutex }
}
var first sync.Mutex
var second = &sync.RWMutex{}
func run() {
  var third = sync.Mutex{}
  fourth := sync.Mutex{}
  _ = fourth
}
|};
  count "comments, literals, method calls and container element types are not declarations" "CS-5" 0 {|package fixture
// var guard sync.Mutex
var example = "sync.Mutex"
var locks []sync.Mutex
func run(lock *sync.Mutex) { lock.Lock(); lock.Unlock() }
|}

let test_dispatches () =
  count "only a method consisting solely of three or more sibling calls" "CS-6" 1 {|package fixture
type checker struct{}
func (check *checker) all() {
  check.first() // comments are harmless
  check.second()
  check.third()
}
func (check *checker) pair() { check.first(); check.second() }
func (check *checker) value() { check.first(); check.second(); check.third(1) }
func (check *checker) foreign() { check.first(); check.second(); other.third() }
func (check *checker) ordered() { check.first(); check.second(); check.third(); return }
func all(check *checker) { check.first(); check.second(); check.third() }
|}

let test_erasure () =
  let source = {|package fixture
type State any
type Alias = interface{}
type IWidget interface {
  Reduce(state State) any
  Decode(value interface{}) Alias
  hidden(value any)
  Batch(values []any) map[string]any
  Spread(values ...any)
}
type Public struct {
  State State
  Value any
  Hidden interface{}
  private any
  Values []any
  Ref *any
}
type private struct { State any }
|} in
  count "exported bare erasure and file-local aliases" "CS-7" 7 source;
  count ~path:"fixture/internal/adapter.go" "internal erasure belongs to its owner" "CS-7" 0 source;
  count "generic type parameter remains concrete at the boundary" "CS-7" 0 {|package fixture
type IWidget[State any] interface { Reduce(state State) State }
type Widget[State any] struct { State State }
|};
  count "a generic API instantiated with any at a public boundary" "CS-7" 5 {|package fixture
type IMessages interface {
  Send(envelope relay.Envelope[any]) relay.Envelope[any]
}
type Messaging struct {
  Messenger *relay.Messenger[any]
  Inbox     []relay.Envelope[interface{}]
  messenger *relay.Messenger[any]
}
func View(envelope relay.Envelope[any]) string { return "" }
func view(envelope relay.Envelope[any]) string { return "" }
|};
  count "a generic API instantiated with its contract is not erased" "CS-7" 0 {|package fixture
type Messaging struct {
  Messenger *relay.Messenger[*agentv1.AgentMessage]
  Pairs     map[string]relay.Envelope[string]
}
func View(envelope relay.Envelope[*agentv1.AgentMessage]) string { return "" }
func Wire[Body any](messenger *relay.Messenger[Body]) {}
|}

let test_sleep () =
  let source = {|package fixture
func poll() {
  time.Sleep(budget)
  for condition {
    if condition { time.Sleep(interval) }
    for range items { time.Sleep(interval) }
  }
}
|} in
  count ~path:"fixture/poll_test.go" "nested test loops each report their sleep once" "CS-9" 2 source;
  count "production pacing is outside test timing rule" "CS-9" 0 source;
  count ~path:"fixture/poll_test.go" "for header is outside the loop body" "CS-9" 0 {|package fixture
func poll() { for time.Sleep(interval); condition; advance() {} }
|}

let test_constructors () =
  count "generic constructor spellings include private variants" "CS-12" 8 {|package fixture
func New() *Thing { return nil }
func new() *Thing { return nil }
func NewStore() *Thing { return nil }
func newStore() *Thing { return nil }
func NewClient() *Thing { return nil }
func newClient() *Thing { return nil }
func NewService() *Thing { return nil }
func newService() *Thing { return nil }
func NewThing() *Thing { return nil }
func (thing *Thing) New() *Thing { return thing }
|}

let test_magic_strings () =
  let source = {|package fixture
import "errors"
const statusIdle = "idle"
var registry = map[string]string{"idle": "running"}
var factory = func() string { return "package_factory" }
type Session struct { Status string `json:"status"` }
func apply() {
  const ended = "ended"
  const (
    failed = "failed"
    stopped = "stopped"
  )
  var current = "queued"
  _ = ""
  _ = "/"
  _ = "\n"
  _ = "é"
  _ = "human facing prose"
  _ = errors.New("unreachable")
  _ = fmt.Sprintf("format:%s", "value_argument")
  fmt.Fprintf(writer, "format:%s", "writer_argument")
  logger.Info("message", "status", "running")
  _ = Finding{Message: "explanation", Reason: "unreachable", Kind: "invalid"}
  writer.Header().Set("Content-Type", "application/json")
  if current == "running" { current = statusIdle }
  values := map[string]string{"state": "running"}
  _ = values
  _ = func() string { return "nested_closure" }
  var local struct { Name string `json:"local"` }
  _ = local
}
|} in
  count "semantic string uses survive message, syntax and declaration exemptions" "CS-13" 10 source;
  count ~path:"fixture/session_test.go" "wire expectations in tests stay literal" "CS-13" 0 source;
  count "const span ends at its declaration, including semicolons" "CS-13" 1 {|package fixture
func run() { const status = "idle"; consume("running") }
|};
  count "message fields do not hide nested semantic arguments" "CS-13" 1 {|package fixture
func run() { _ = Finding{Message: translate("lookup_key")} }
|};
  let located = findings "CS-13" "package fixture\nfunc run() {\n  consume(\"idle\")\n}\n" in
  expect "magic strings retain the source line" (List.map (fun (finding : Source.finding) -> finding.line) located = [3])

let test_null_twins () =
  let source = {|package fixture
func optionalTime(value sql.NullTime) *time.Time { return nil }
func nullableString(value string) sql.NullString { return sql.NullString{} }
func optionalInt(value *sql.NullInt16) int { return 0 }
func nullableUUID(value *uuid.UUID) uuid.NullUUID { return uuid.NullUUID{} }
func normalize(value sql.NullTime) sql.NullTime { return value }
func carry(value sql.NullTime) *Row { return nil }
func multiple(first, second string) sql.NullString { return sql.NullString{} }
func results(value string) (sql.NullString, error) { return sql.NullString{}, nil }
func grouped(value sql.NullTime) (first, second time.Time) { return }
func variadic(values ...string) sql.NullString { return sql.NullString{} }
func (row *Row) optionalTime(value sql.NullTime) *time.Time { return nil }
|} in
  count "single argument/result optional conversions in both directions" "CS-14" 4 source;
  count ~path:"fixture/optional_test.go" "test conversions are evidence" "CS-14" 0 source

let test_handler_db_io () =
  count ~path:"fixture/handlers.go"
    "in-memory stores do not declare database I/O" "HANDLER-DB-IO" 0 {|package fixture
type LinkStore struct { links map[string]string }
type Handler struct { store *LinkStore }
func (handler *Handler) Get() { handler.store.GetLink("name") }
|};
  count ~path:"fixture/handlers.go"
    "store-derived values in logging arguments do not make the logger a database receiver" "HANDLER-DB-IO" 2 {|package fixture
import "database/sql"
func Serve(storage *sql.DB) {
  row, _ := storage.Query("select")
  logger.Info().Any("row", row.Value).Str("state", "done").Msg("read complete")
}
|};
  let storage = Source.parse "fixture/storage.go" {|package fixture
import "database/sql"
type Service struct { store *sql.DB }
|} in
  let boundary = Source.parse "fixture/handlers.go" {|package fixture
func Serve(service *Service) { service.store.Query("select") }
|} in
  expect "database imports in a sibling file establish the package boundary"
    (Advisory.collect [storage; boundary] |> List.filter (fun (finding : Source.finding) ->
      finding.rule = "HANDLER-DB-IO") |> List.length = 1);
  count ~path:"services/copilot-adapter/sse.go"
    "API receiver storage calls, chained storage receivers and nested callbacks are checked" "HANDLER-DB-IO" 3 {|package fixture
import (
  "net/http"
  storedb "example.invalid/copilot/storedb"
)
type Service struct { store *storedb.Queries }
type apiHandlers struct { service *Service; store *storedb.Queries }
func (handler *apiHandlers) Serve(request *http.Request) {
  _ = request.URL.Query()
  handler.service.FindSession(request.Context())
  handler.store.GetSession(request.Context())
  eventStream(func(writer http.ResponseWriter) bool {
    handler.service.store.Query(request.Context(), "select")
    return false
  })
}
|};
  count "strict-interface operation signatures are scoped outside handler filenames" "HANDLER-DB-IO" 1 {|package fixture
import (
  "context"
  database "database/sql"
  api "example.invalid/api"
)
func CreateSession(request api.CreateSessionRequestObject) api.CreateSessionResponseObject {
  _, _ = database.Open("postgres", "")
  return api.CreateSessionResponseObject{}
}
func UpdateSession(request api.UpdateSessionRequestObject) api.UpdateSessionResponseObject {
  _ = context.Background()
  return api.UpdateSessionResponseObject{}
}
|};
  count "typed and aliased database handles and transaction chains are detected" "HANDLER-DB-IO" 3 {|package fixture
import (
  "net/http"
  database "database/sql"
)
type apiHandlers struct{}
func (handler *apiHandlers) Serve(request *http.Request, storage *database.DB) {
  transaction, _ := storage.BeginTx(request.Context(), nil)
  transaction.ExecContext(request.Context(), "update sessions")
}
|};
  count "service operations, request parsing, and row/view DTOs remain allowed" "HANDLER-DB-IO" 0 {|package fixture
import (
  "net/http"
  storedb "example.invalid/copilot/storedb"
)
type Service struct{}
type apiHandlers struct { service *Service }
type SessionView struct { ID string }
func (service *Service) MapRow(row storedb.SessionRow) SessionView { return SessionView{ID: row.ID} }
func (handler *apiHandlers) Serve(request *http.Request) {
  values := request.URL.Query()
  _ = values.Get("session_id")
  row := storedb.SessionRow{}
  _ = handler.service.MapRow(row)
}
|};
  count ~path:"services/copilot-adapter/handler_test.go" "handler tests are outside the production boundary" "HANDLER-DB-IO" 0 {|package fixture
import "database/sql"
func TestHandler(database *sql.DB) { database.Query("select") }
|}

let test_import_aliases () =
  count "mutex aliases include short declarations" "CS-5" 2 {|package fixture
import locks "sync"
type State struct { Guard locks.Mutex }
func run() { guard := &locks.RWMutex{}; _ = guard }
|};
  count ~path:"fixture/poll_test.go" "test timing follows aliases" "CS-9" 1 {|package fixture
import clock "time"
func run() { for condition { clock.Sleep(budget) } }
|};
  count "optional conversion aliases follow their imports" "CS-14" 1 {|package fixture
import (
  database "database/sql"
  clock "time"
)
func optionalTime(value database.NullTime) *clock.Time { return nil }
|};
  count "human error messages retain aliases" "CS-13" 0 {|package fixture
import failure "errors"
func run() { _ = failure.New("unreachable") }
|};
  count "a different package called sync is not the stdlib primitive" "CS-5" 0 {|package fixture
import sync "example.invalid/sync"
var guard sync.Mutex
|}

let test_corpus_scope () =
  let source = "package fixture\nfunc New() {}\n" in
  List.iter (fun path -> count ~path "excluded corpus path" "CS-12" 0 source)
    ["research/fixture.go"; "vendor/fixture.go"; "nested/vendor/fixture.go"];
  count "generated output belongs to its generator" "CS-12" 0
    ("// Code generated by fixture. DO NOT EDIT.\n" ^ source)

let test_first_error_latches () =
  count "a first-error latch filled by a non-blocking send" "CS-14" 2 {|package fixture
func run(work func() error) {
  failures := make(chan error, 1)
  report := func(err error) {
    select {
    case failures <- err:
    default:
    }
  }
  var latch = make(chan error, 1)
  select {
  case latch <- work():
  default:
  }
  report(nil)
}
|};
  count "blocking sends, other channels and other capacities are not latches" "CS-14" 0 {|package fixture
func run(work func() error, stop chan struct{}) {
  done := make(chan error, 1)
  done <- work()
  select {
  case done <- work():
  case <-stop:
  }
  wide := make(chan error, 4)
  select {
  case wide <- work():
  default:
  }
  values := make(chan int, 1)
  select {
  case values <- 1:
  default:
  }
}
|};
  count ~path:"fixture/latch_test.go" "tests are out of scope" "CS-14" 0 {|package fixture
func run(err error) {
  failures := make(chan error, 1)
  select {
  case failures <- err:
  default:
  }
}
|}

let () =
  test_mutexes ();
  test_first_error_latches ();
  test_dispatches ();
  test_erasure ();
  test_sleep ();
  test_constructors ();
  test_magic_strings ();
  test_null_twins ();
  test_handler_db_io ();
  test_import_aliases ();
  test_corpus_scope ();
  print_endline "Advisory house rule OCaml tests passed"
