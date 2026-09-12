package trader

import (
	"encoding/json"
	"fmt"
	"nofx/store"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStoreWithPath(t *testing.T) (*store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test_health.db")
	st, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st, dbPath
}

func TestSafeModePersistence_DurableLatchRestoredAcrossRestart(t *testing.T) {
	st, dbPath := newTestStoreWithPath(t)
	traderID := "trader_persistent_1"
	userID := "user_alpha"

	// 1. First instance: triggers protection failure
	at1 := &AutoTrader{
		id:     traderID,
		userID: userID,
		store:  st,
	}

	failureReason := "opening stop-loss placement failed after 3 retries and emergency exit failed"
	if err := at1.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, failureReason); err != nil {
		t.Fatalf("setSafeModeWithCause failed: %v", err)
	}

	key1, keyErr := at1.safeModeStoreKey()
	if keyErr != nil {
		t.Fatalf("safeModeStoreKey failed: %v", keyErr)
	}

	// Verify database record directly
	raw, err := st.GetSystemConfig(key1)
	if err != nil || raw == "" {
		t.Fatalf("expected persistent safe mode record in store, got raw=%s, err=%v", raw, err)
	}

	var state PersistedSafeModeState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		t.Fatalf("failed to unmarshal persisted record: %v", err)
	}
	if !state.Active || state.Cause != SafeModeCauseProtectionFailure || state.TraderID != traderID || state.Version != 1 {
		t.Fatalf("unexpected persisted state: %+v", state)
	}
	originalTime := state.UpdatedAt

	// 2. Simulate complete process restart: close SQLite database and reopen it from disk
	if err := st.Close(); err != nil {
		t.Fatalf("failed to close store: %v", err)
	}

	stReopened, err := store.New(dbPath)
	if err != nil {
		t.Fatalf("failed to reopen store from disk: %v", err)
	}
	defer stReopened.Close()

	at2 := &AutoTrader{
		id:     traderID,
		userID: userID,
		store:  stReopened,
	}

	if at2.isSafeMode() {
		t.Fatalf("expected at2 initially not in safe mode before restore")
	}

	if err := at2.restorePersistedSafeMode(); err != nil {
		t.Fatalf("restorePersistedSafeMode returned error: %v", err)
	}

	if !at2.isSafeMode() {
		t.Fatalf("expected at2 to be locked in safe mode after restart recovery")
	}
	if at2.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected at2 cause %s, got %s", SafeModeCauseProtectionFailure, at2.getSafeModeCause())
	}
	_, reason := at2.safeModeState()
	if reason != failureReason {
		t.Fatalf("expected restored reason to match failureReason exactly without duplicate prefixes, got: %s", reason)
	}

	// 3. Subsequent restore check: must be purely read-only (bytes and timestamp unchanged)
	if err := at2.restorePersistedSafeMode(); err != nil {
		t.Fatalf("second restorePersistedSafeMode returned error: %v", err)
	}
	rawAfter, _ := stReopened.GetSystemConfig(key1)
	if rawAfter != raw {
		t.Fatalf("expected store payload to remain unchanged across repeated restores")
	}
	var stateAfter PersistedSafeModeState
	_ = json.Unmarshal([]byte(rawAfter), &stateAfter)
	if !stateAfter.UpdatedAt.Equal(originalTime) {
		t.Fatalf("restore must not rewrite original event timestamp: before=%v, after=%v", originalTime, stateAfter.UpdatedAt)
	}
}

func TestSafeModePersistence_WriteFailureKeepsMemoryLockedAndReturnsError(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	at := &AutoTrader{
		id:     "trader_write_fail",
		userID: "user_alpha",
		store:  st,
	}

	// Close database to force write failure
	_ = st.Close()

	err := at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "SL failed on symbol BTC")
	if err == nil {
		t.Fatalf("expected setSafeModeWithCause to return error when database write fails")
	}

	// Critical invariant: In-memory safe mode MUST remain locked even if disk write fails!
	if !at.isSafeMode() {
		t.Fatalf("expected in-memory safe mode to remain locked despite database write failure")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected cause to remain ProtectionFailure")
	}
}

func TestSafeModePersistence_ReadFailureEntersSafeModeAndReturnsError(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	at := &AutoTrader{
		id:     "trader_read_fail",
		userID: "user_alpha",
		store:  st,
	}

	// Close database to force read failure
	_ = st.Close()

	err := at.restorePersistedSafeMode()
	if err == nil {
		t.Fatalf("expected restorePersistedSafeMode to return error when database read fails")
	}

	// In-memory safe mode MUST enter conservative lock on unreadable state
	if !at.isSafeMode() {
		t.Fatalf("expected in-memory safe mode to be locked when safe mode state is unreadable")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected cause to be ProtectionFailure")
	}
}

func TestSafeModePersistence_AIRecoveryCannotClearProtectionLatch(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	traderID := "trader_persistent_2"
	userID := "user_alpha"

	at := &AutoTrader{
		id:     traderID,
		userID: userID,
		store:  st,
	}

	_ = at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "critical protection failure")

	// AI completes a normal successful cycle and tries to auto-recover safe mode
	deactivated := at.tryDeactivateSafeModeOnAIRecovery()
	if deactivated {
		t.Fatalf("tryDeactivateSafeModeOnAIRecovery should return false for protection failure")
	}

	if !at.isSafeMode() {
		t.Fatalf("expected safe mode to remain active")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected cause to remain ProtectionFailure")
	}

	key, _ := at.safeModeStoreKey()
	raw, _ := st.GetSystemConfig(key)
	if raw == "" {
		t.Fatalf("expected persistent latch to remain in store")
	}
}

func TestSafeModePersistence_TransientFailureCannotDowngradeProtectionLatch(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	at := &AutoTrader{
		id:     "trader_persistent_3",
		userID: "user_alpha",
		store:  st,
	}

	_ = at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "hard protection failure")

	// Subsequent cycle experiences a transient AI error or timeout
	_ = at.setSafeModeWithCause(true, SafeModeCauseAI, "transient ai 503 error")

	// Must NOT downgrade the cause from ProtectionFailure to AI
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected cause to remain ProtectionFailure, got: %s", at.getSafeModeCause())
	}
	_, reason := at.safeModeState()
	if !strings.Contains(reason, "hard protection failure") {
		t.Fatalf("expected reason not to be overwritten by transient error, got: %s", reason)
	}
}

func TestSafeModePersistence_GenericDeactivationRefusesToClearProtectionLatch(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	at := &AutoTrader{
		id:     "trader_persistent_4",
		userID: "user_alpha",
		store:  st,
	}

	_ = at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "hard protection failure")

	// 1. Generic deactivation call (active=false, cause=SafeModeCauseNone) must be refused
	err1 := at.setSafeModeWithCause(false, SafeModeCauseNone, "")
	if err1 == nil {
		t.Fatalf("expected generic deactivation of ProtectionFailure to return error")
	}

	// 2. Generic deactivation call passing transient cause (active=false, cause=SafeModeCauseAI) must ALSO hit Guard 2 and be refused
	err2 := at.setSafeModeWithCause(false, SafeModeCauseAI, "")
	if err2 == nil {
		t.Fatalf("expected transient-flavored deactivation of ProtectionFailure to return error")
	}

	if !at.isSafeMode() {
		t.Fatalf("protection safe mode must remain active against generic deactivation")
	}

	key, _ := at.safeModeStoreKey()
	raw, _ := st.GetSystemConfig(key)
	if raw == "" {
		t.Fatalf("expected latch to remain in database")
	}

	// 3. Even if in-memory active is false, setSafeModeWithCause(false, ...) must NEVER wipe durable store latch
	atMemoryClean := &AutoTrader{
		id:     "trader_persistent_4",
		userID: "user_alpha",
		store:  st,
	}
	_ = atMemoryClean.setSafeModeWithCause(false, SafeModeCauseAI, "")
	rawStillThere, _ := st.GetSystemConfig(key)
	if rawStillThere == "" {
		t.Fatalf("setSafeModeWithCause must never wipe persistent database latch")
	}
}

func TestSafeModePersistence_CorruptRecordAndSchemaMismatchTriggerConservativeSafeMode(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	traderID := "trader_schema_test"
	userID := "user_alpha"

	tests := []struct {
		name    string
		payload string
	}{
		{
			name:    "corrupt-json",
			payload: "invalid-json-{broken",
		},
		{
			name:    "empty-object",
			payload: "{}",
		},
		{
			name:    "unknown-version",
			payload: `{"version":2,"user_id":"user_alpha","trader_id":"trader_schema_test","active":true,"cause":"protection_failure","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "mismatched-trader-id",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_other","active":true,"cause":"protection_failure","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "mismatched-user-id",
			payload: `{"version":1,"user_id":"user_beta","trader_id":"trader_schema_test","active":true,"cause":"protection_failure","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "invalid-cause",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_schema_test","active":true,"cause":"unknown_cause","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "empty-cause-when-active",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_schema_test","active":true,"cause":"","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "zero-timestamp-when-active",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_schema_test","active":true,"cause":"protection_failure","reason":"test"}`,
		},
		{
			name:    "empty-reason-when-active",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_schema_test","active":true,"cause":"protection_failure","reason":"   ","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "omitted-active-field",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_schema_test","cause":"protection_failure","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "explicit-active-false",
			payload: `{"version":1,"user_id":"user_alpha","trader_id":"trader_schema_test","active":false,"cause":"protection_failure","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
		{
			name:    "null-payload",
			payload: "null",
		},
		{
			name:    "empty-user-when-configured",
			payload: `{"version":1,"user_id":"","trader_id":"trader_schema_test","active":true,"cause":"protection_failure","reason":"test","updated_at":"2026-09-05T12:00:00Z"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at := &AutoTrader{
				id:     traderID,
				userID: userID,
				store:  st,
			}

			key, keyErr := at.safeModeStoreKey()
			if keyErr != nil {
				t.Fatalf("safeModeStoreKey failed: %v", keyErr)
			}

			if err := st.SetSystemConfig(key, tc.payload); err != nil {
				t.Fatalf("failed to write payload: %v", err)
			}

			err := at.restorePersistedSafeMode()
			if err == nil {
				t.Fatalf("expected restore to return error for %s", tc.name)
			}

			// Must enter conservative safe mode and block trading
			if !at.isSafeMode() {
				t.Fatalf("expected trader to enter conservative safe mode on %s", tc.name)
			}
			if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
				t.Fatalf("expected cause to be ProtectionFailure, got: %s", at.getSafeModeCause())
			}

			// Record in store must NOT be erased automatically (preserves forensic evidence)
			raw, _ := st.GetSystemConfig(key)
			if raw != tc.payload {
				t.Fatalf("payload should not be silently wiped from store on %s", tc.name)
			}
		})
	}
}

func TestSafeModePersistence_KeyIsolationAcrossTradersAndUsers(t *testing.T) {
	st, _ := newTestStoreWithPath(t)

	// Test with colons in IDs to verify URL escaping avoids collision
	atUser1TraderA := &AutoTrader{id: "trader:A", userID: "user:1", store: st}
	atUser1TraderB := &AutoTrader{id: "trader:B", userID: "user:1", store: st}
	atUser2TraderA := &AutoTrader{id: "trader:A", userID: "user:2", store: st}

	_ = atUser1TraderA.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "user1 traderA failed")

	// trader_B under user_1 must be clean
	if err := atUser1TraderB.restorePersistedSafeMode(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atUser1TraderB.isSafeMode() {
		t.Fatalf("trader_B should not be affected by trader_A")
	}

	// trader_A under user_2 must be clean
	if err := atUser2TraderA.restorePersistedSafeMode(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atUser2TraderA.isSafeMode() {
		t.Fatalf("user_2 trader_A should not be affected by user_1")
	}

	// user_1 trader_A must be locked
	atUser1TraderA_Reload := &AutoTrader{id: "trader:A", userID: "user:1", store: st}
	if err := atUser1TraderA_Reload.restorePersistedSafeMode(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !atUser1TraderA_Reload.isSafeMode() {
		t.Fatalf("user_1 trader_A must be locked")
	}
}

func TestSafeModePersistence_ConcurrentActivationAndRecoveryNoRace(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	at := &AutoTrader{
		id:     "trader_race_1",
		userID: "user_alpha",
		store:  st,
	}

	var wg sync.WaitGroup
	workers := 10

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if workerID%2 == 0 {
					at.tryDeactivateSafeModeOnAIRecovery()
				} else {
					_ = at.setSafeModeWithCause(true, SafeModeCauseAI, fmt.Sprintf("worker %d ai error", workerID))
				}
			}
		}(i)
	}

	wg.Wait()

	// Trigger a hard protection failure concurrently with AI deactivations
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if workerID == 0 {
				_ = at.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "hard protection failure")
			} else {
				at.tryDeactivateSafeModeOnAIRecovery()
			}
		}(i)
	}

	wg.Wait()

	// Once a ProtectionFailure is set, it cannot be cleared by tryDeactivateSafeModeOnAIRecovery
	if !at.isSafeMode() {
		t.Fatalf("expected safe mode to be active after protection failure")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected cause to be ProtectionFailure, got: %s", at.getSafeModeCause())
	}
}

func TestSafeModePersistence_DefaultUserIDNormalizationConsistent(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	traderID := "trader_norm_user"

	// Instance 1 created with empty userID (defaults to "default")
	at1 := &AutoTrader{
		id:     traderID,
		userID: "",
		store:  st,
	}
	_ = at1.setSafeModeWithCause(true, SafeModeCauseProtectionFailure, "SL failed on symbol BTC")

	// Instance 2 reloaded with explicit "default" userID
	at2 := &AutoTrader{
		id:     traderID,
		userID: "default",
		store:  st,
	}
	if err := at2.restorePersistedSafeMode(); err != nil {
		t.Fatalf("expected restore to succeed with normalized default user, got: %v", err)
	}
	if !at2.isSafeMode() || at2.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected safe mode to be restored with normalized user")
	}
}

func TestSafeModePersistence_EmptyTraderIDTriggersSafeMode(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	at := &AutoTrader{
		id:     "   ",
		userID: "user_alpha",
		store:  st,
	}

	err := at.restorePersistedSafeMode()
	if err == nil {
		t.Fatalf("expected error on empty trader ID")
	}
	if !at.isSafeMode() || at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected trader with blank ID to be placed in safe mode")
	}
}

func TestSafeModePersistence_NewAutoTraderEarlyRejectionOnCorruptStore(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	traderID := "trader_constructor_test"
	userID := "user_alpha"

	corruptKey := fmt.Sprintf("safemode:v1:%s:%s", userID, traderID)
	if err := st.SetSystemConfig(corruptKey, "{corrupt-json"); err != nil {
		t.Fatalf("failed to seed corrupt config: %v", err)
	}

	cfg := AutoTraderConfig{
		ID:            traderID,
		Name:          "Constructor Fail Test",
		AIModel:       "deepseek",
		CustomAPIKey:  "sk-dummy",
		Exchange:      "binance",
		ExecutionMode: ExecutionModePaper,
	}

	_, err := NewAutoTrader(cfg, st, userID)
	if err == nil {
		t.Fatalf("expected NewAutoTrader to abort constructor when safe mode state in database is corrupt")
	}
	if !strings.Contains(err.Error(), "safe mode state in database is invalid or unreadable") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestSafeModePersistence_NewAutoTraderRestoresValidProtectionLatch(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	traderID := "trader_restore_test"
	userID := "user_alpha"
	latchReason := "opening SL placement failed and emergency close incomplete"

	validPayload := fmt.Sprintf(`{"version":1,"user_id":"%s","trader_id":"%s","active":true,"cause":"protection_failure","reason":"%s","updated_at":"2026-09-05T12:00:00Z"}`,
		userID, traderID, latchReason)

	key := fmt.Sprintf("safemode:v1:%s:%s", userID, traderID)
	if err := st.SetSystemConfig(key, validPayload); err != nil {
		t.Fatalf("failed to seed valid config: %v", err)
	}

	strategy := store.GetDefaultStrategyConfig("en")
	cfg := AutoTraderConfig{
		ID:             traderID,
		Name:           "Restore Success Test",
		AIModel:        "deepseek",
		CustomAPIKey:   "sk-dummy",
		Exchange:       "binance",
		ExecutionMode:  ExecutionModePaper,
		StrategyConfig: &strategy,
	}

	at, err := NewAutoTrader(cfg, st, userID)
	if err != nil {
		t.Fatalf("expected NewAutoTrader to succeed and restore latch, got error: %v", err)
	}

	// Must start actively locked in safe mode
	if !at.isSafeMode() {
		t.Fatalf("expected NewAutoTrader to be locked in safe mode on startup")
	}
	if at.getSafeModeCause() != SafeModeCauseProtectionFailure {
		t.Fatalf("expected cause to be ProtectionFailure, got: %s", at.getSafeModeCause())
	}
	_, reason := at.safeModeState()
	if reason != latchReason {
		t.Fatalf("expected restored reason to match exactly, got: %s", reason)
	}
}

func TestSafeModePersistence_NewAutoTraderRejectsClosedDatabase(t *testing.T) {
	st, _ := newTestStoreWithPath(t)
	traderID := "trader_closed_db_test"
	userID := "user_alpha"

	// Close database to force read failure during NewAutoTrader
	_ = st.Close()

	cfg := AutoTraderConfig{
		ID:            traderID,
		Name:          "Closed DB Test",
		AIModel:       "deepseek",
		CustomAPIKey:  "sk-dummy",
		Exchange:      "binance",
		ExecutionMode: ExecutionModePaper,
	}

	_, err := NewAutoTrader(cfg, st, userID)
	if err == nil {
		t.Fatalf("expected NewAutoTrader to fail when database is closed")
	}
	if !strings.Contains(err.Error(), "safe mode state in database is invalid or unreadable") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
