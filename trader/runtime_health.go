package trader

import (
	"encoding/json"
	"fmt"
	"net/url"
	"nofx/logger"
	"strings"
	"time"
)

// Runtime health state written by the run-loop goroutine and read by the API
// layer (GetStatus). Everything here goes through runtimeHealthMu so the
// dashboard can poll without racing the trading loop.

// SafeModeCause distinguishes what activated safe mode.
// Only automated transient conditions (like AI API failures or cycle timeouts)
// may be automatically deactivated by subsequent successful cycles.
// Hard risk and protection failures require explicit administrative review.
type SafeModeCause string

const (
	SafeModeCauseNone              SafeModeCause = ""
	SafeModeCauseAI                SafeModeCause = "ai_failure"
	SafeModeCauseTimeout           SafeModeCause = "cycle_timeout"
	SafeModeCauseProtectionFailure SafeModeCause = "protection_failure"
)

func (c SafeModeCause) CanAutoRecover() bool {
	return c == SafeModeCauseAI || c == SafeModeCauseTimeout
}

func (c SafeModeCause) IsValid() bool {
	return c == SafeModeCauseNone || c == SafeModeCauseAI || c == SafeModeCauseTimeout || c == SafeModeCauseProtectionFailure
}

// PersistedSafeModeState represents a versioned, durable latch for non-recoverable safe mode.
type PersistedSafeModeState struct {
	Version   int           `json:"version"`
	UserID    string        `json:"user_id"`
	TraderID  string        `json:"trader_id"`
	Active    bool          `json:"active"`
	Cause     SafeModeCause `json:"cause"`
	Reason    string        `json:"reason"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func (at *AutoTrader) normalizedUserID() string {
	if at == nil {
		return "default"
	}
	u := strings.TrimSpace(at.userID)
	if u == "" {
		return "default"
	}
	return u
}

func (at *AutoTrader) safeModeStoreKey() (string, error) {
	if at == nil || strings.TrimSpace(at.id) == "" {
		return "", fmt.Errorf("trader ID cannot be empty for safe mode persistence")
	}
	userID := at.normalizedUserID()
	return fmt.Sprintf("safemode:v1:%s:%s", url.QueryEscape(userID), url.QueryEscape(strings.TrimSpace(at.id))), nil
}

func (at *AutoTrader) setSafeModeWithCause(active bool, cause SafeModeCause, reason string) error {
	at.safeModeTransitionMu.Lock()
	defer at.safeModeTransitionMu.Unlock()

	at.runtimeHealthMu.Lock()
	// Guard 1: If current state is a non-auto-recoverable cause (like ProtectionFailure),
	// a transient cause (like AI failure or timeout) MUST NOT downgrade or overwrite it!
	if active && at.safeMode && !at.safeModeCause.CanAutoRecover() && cause.CanAutoRecover() {
		at.runtimeHealthMu.Unlock()
		return nil
	}

	// Guard 2: If current state is a non-auto-recoverable cause, generic deactivation (active=false)
	// MUST NOT clear the protection latch!
	if !active && at.safeMode && !at.safeModeCause.CanAutoRecover() {
		at.runtimeHealthMu.Unlock()
		logger.Warnf("🚨 [%s] Refusing to clear non-recoverable SafeMode (%s); requires explicit administrative intervention", at.id, at.safeModeCause)
		return fmt.Errorf("cannot clear non-recoverable safe mode (%s) via generic deactivation", at.safeModeCause)
	}

	at.safeMode = active
	if active {
		at.safeModeCause = cause
		at.safeModeReason = reason
	} else {
		at.safeModeCause = SafeModeCauseNone
		at.safeModeReason = ""
	}
	at.runtimeHealthMu.Unlock()

	// Persist non-auto-recoverable latches to store
	if at.store != nil {
		key, keyErr := at.safeModeStoreKey()
		if keyErr != nil {
			logger.Errorf("🚨 Failed to generate safe mode store key for %s: %v", at.id, keyErr)
			return keyErr
		}

		if active && !cause.CanAutoRecover() {
			state := PersistedSafeModeState{
				Version:   1,
				UserID:    at.normalizedUserID(),
				TraderID:  strings.TrimSpace(at.id),
				Active:    true,
				Cause:     cause,
				Reason:    reason,
				UpdatedAt: time.Now().UTC(),
			}
			bytes, err := json.Marshal(state)
			if err != nil {
				return fmt.Errorf("marshal safe mode latch failed: %w", err)
			}
			if setErr := at.store.SetSystemConfig(key, string(bytes)); setErr != nil {
				logger.Errorf("🚨 Failed to persist SafeMode latch for %s: %v (memory remains locked in safe mode)", at.id, setErr)
				return fmt.Errorf("persist safe mode latch failed: %w", setErr)
			}
		}
	}
	return nil
}

// restorePersistedSafeMode restores durable safe mode latch from store during trader initialization.
// Read-only operation: applies stored state directly without writing back to the database.
func (at *AutoTrader) restorePersistedSafeMode() error {
	if at == nil || at.store == nil {
		return nil
	}

	at.safeModeTransitionMu.Lock()
	defer at.safeModeTransitionMu.Unlock()

	key, keyErr := at.safeModeStoreKey()
	if keyErr != nil {
		at.runtimeHealthMu.Lock()
		at.safeMode = true
		at.safeModeCause = SafeModeCauseProtectionFailure
		at.safeModeReason = fmt.Sprintf("invalid trader ID for safe mode persistence: %v", keyErr)
		at.runtimeHealthMu.Unlock()
		return keyErr
	}

	raw, err := at.store.GetSystemConfig(key)
	if err != nil {
		at.runtimeHealthMu.Lock()
		at.safeMode = true
		at.safeModeCause = SafeModeCauseProtectionFailure
		at.safeModeReason = fmt.Sprintf("failed to read safe mode state from database: %v", err)
		at.runtimeHealthMu.Unlock()
		return fmt.Errorf("read safe mode config failed: %w", err)
	}

	if raw == "" {
		return nil
	}

	var state PersistedSafeModeState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		at.runtimeHealthMu.Lock()
		at.safeMode = true
		at.safeModeCause = SafeModeCauseProtectionFailure
		at.safeModeReason = fmt.Sprintf("corrupt safe mode record in database: %v", err)
		at.runtimeHealthMu.Unlock()
		return fmt.Errorf("corrupt safe mode record: %w", err)
	}

	// In this version, durable storage only records active, unrecoverable ProtectionFailure latches.
	// Any stored record MUST have Version == 1, TraderID matching at.id, UserID matching at.normalizedUserID(),
	// Active == true, Cause == SafeModeCauseProtectionFailure, a valid nonzero UpdatedAt, and a nonblank Reason.
	// Inactive records (Active == false) or omitted fields are strictly invalid and rejected.
	expectedUserID := at.normalizedUserID()
	isInvalidState := state.Version != 1 ||
		state.TraderID != strings.TrimSpace(at.id) ||
		state.UserID != expectedUserID ||
		!state.Active ||
		state.Cause != SafeModeCauseProtectionFailure ||
		state.UpdatedAt.IsZero() ||
		strings.TrimSpace(state.Reason) == ""

	if isInvalidState {
		at.runtimeHealthMu.Lock()
		at.safeMode = true
		at.safeModeCause = SafeModeCauseProtectionFailure
		at.safeModeReason = fmt.Sprintf("invalid safe mode record schema/identity in database (ver=%d, user=%s, trader=%s, cause=%s)",
			state.Version, state.UserID, state.TraderID, state.Cause)
		at.runtimeHealthMu.Unlock()
		return fmt.Errorf("invalid safe mode record schema or identity mismatch")
	}

	if state.Active && !state.Cause.CanAutoRecover() {
		// Read-only apply: update memory directly without rewriting store or timestamp
		at.runtimeHealthMu.Lock()
		at.safeMode = true
		at.safeModeCause = state.Cause
		at.safeModeReason = state.Reason
		at.runtimeHealthMu.Unlock()

		logger.Warnf("🛡️ [%s] Restored durable SafeMode latch from database (cause: %s, locked since: %s): %s",
			at.id, state.Cause, state.UpdatedAt.Format(time.RFC3339), state.Reason)
	}

	return nil
}

func (at *AutoTrader) setSafeMode(active bool, reason string) {
	_ = at.setSafeModeWithCause(active, SafeModeCauseAI, reason)
}

func (at *AutoTrader) isSafeMode() bool {
	at.runtimeHealthMu.RLock()
	defer at.runtimeHealthMu.RUnlock()
	return at.safeMode
}

func (at *AutoTrader) safeModeState() (bool, string) {
	at.runtimeHealthMu.RLock()
	defer at.runtimeHealthMu.RUnlock()
	return at.safeMode, at.safeModeReason
}

func (at *AutoTrader) getSafeModeCause() SafeModeCause {
	at.runtimeHealthMu.RLock()
	defer at.runtimeHealthMu.RUnlock()
	return at.safeModeCause
}

// tryDeactivateSafeModeOnAIRecovery deactivates safe mode atomically if the underlying cause
// is transient (e.g. AI API failures or timeouts) rather than an unrecoverable protection failure.
func (at *AutoTrader) tryDeactivateSafeModeOnAIRecovery() bool {
	at.safeModeTransitionMu.Lock()
	defer at.safeModeTransitionMu.Unlock()

	at.runtimeHealthMu.Lock()
	defer at.runtimeHealthMu.Unlock()

	if !at.safeMode {
		return false
	}
	if at.safeModeCause.CanAutoRecover() {
		at.safeMode = false
		at.safeModeCause = SafeModeCauseNone
		at.safeModeReason = ""
		at.logInfof("🛡️ SAFE MODE DEACTIVATED — AI is working again. Resuming normal trading.")
		return true
	}
	at.logWarnf("🛡️ Safe mode remains active due to non-recoverable condition (%s): %s", at.safeModeCause, at.safeModeReason)
	return false
}
