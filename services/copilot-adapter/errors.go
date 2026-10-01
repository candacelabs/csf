package copilotadapter

import (
	"database/sql"
	"errors"
	"net/http"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// Error codes the adapter writes into the contract's Error.code field. A
// client dispatches on the code; the message beside it is for a human.
const (
	errorCodeAbortTargetChanged     = "abort_target_changed"
	errorCodeCLIAbortFailed         = "cli_abort_failed"
	errorCodeCLICloseFailed         = "cli_close_failed"
	errorCodeCLIModelSwitchFailed   = "cli_model_switch_failed"
	errorCodeCLIResolveFailed       = "cli_resolve_failed"
	errorCodeCLISendFailed          = "cli_send_failed"
	errorCodeCLIUnavailable         = "cli_unavailable"
	errorCodeEmptyPatch             = "empty_patch"
	errorCodeInvalidBody            = "invalid_body"
	errorCodeInvalidCursor          = "invalid_cursor"
	errorCodeInvalidRequest         = "invalid_request"
	errorCodeIdempotencyKeyReused   = "idempotency_key_reused"
	errorCodeNoTurnInFlight         = "no_turn_in_flight"
	errorCodeRequestAlreadyResolved = "request_already_resolved"
	errorCodeRequestNotFound        = "request_not_found"
	errorCodeSessionNotFound        = "session_not_found"
	errorCodeSessionNotLive         = "session_not_live"
	errorCodeSessionNotReady        = "session_not_ready"
	errorCodeSessionTerminal        = "session_terminal"
	errorCodeStoreError             = "store_error"
	errorCodeStreamUnavailable      = "stream_unavailable"
	errorCodeWorktreePrepareFailed  = "worktree_prepare_failed"
)

// failure is the one error shape the handlers return for a contract-declared
// status; renderFailures turns it into the Error body once, so no handler
// spells a response envelope by hand.
type failure struct {
	status  int
	code    string
	message string
}

func (f failure) Error() string { return f.code + ": " + f.message }

func fail(status int, code string, message string) error {
	return failure{status: status, code: code, message: message}
}

// storeFailure is the 500 every persistence error maps to.
func storeFailure(err error) error {
	return fail(http.StatusInternalServerError, errorCodeStoreError, err.Error())
}

// lookupFailure maps a missing row to the contract's 404 and anything else
// to a 500.
func lookupFailure(err error, code string, message string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fail(http.StatusNotFound, code, message)
	}
	return storeFailure(err)
}

func rejectStartingSession(row storedb.Session) error {
	if row.Status == string(api.SessionStatusStarting) {
		return fail(http.StatusConflict, errorCodeSessionNotReady, "the session is still starting")
	}
	return nil
}
